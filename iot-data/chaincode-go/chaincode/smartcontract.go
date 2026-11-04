package chaincode

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

// 设备状态：0=待激活 1=正常 2=已吊销
const (
	StatusPending = 0
	StatusNormal  = 1
	StatusRevoked = 2
)

// Device 设备实体
type Device struct {
	DeviceID     string `json:"deviceId"`
	Owner        string `json:"owner"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	PublicKey    string `json:"publicKey"` // hex 编码的 PKIX/DER ECDSA(P-256) 公钥
	Status       int    `json:"status"`    // 0=待激活 1=正常 2=已吊销
	RegisteredAt int64  `json:"registeredAt"`
	RevokedAt    int64  `json:"revokedAt"`
}

// Authorization 数据访问授权：设备所有者将某设备的数据读取权授予数据消费方
type Authorization struct {
	DeviceID  string `json:"deviceId"`
	Consumer  string `json:"consumer"`  // 数据消费方身份标识（链码侧 caller ID）
	GrantedAt int64  `json:"grantedAt"`
	RevokedAt int64  `json:"revokedAt"` // 0 表示未撤销
	Active    bool   `json:"active"`
}

// DataRecord 设备直连上报的数据记录
type DataRecord struct {
	RecordID  string `json:"recordId"`
	DeviceID  string `json:"deviceId"`
	DataType  string `json:"dataType"`  // 数据类型，如 temperature / humidity
	Value     string `json:"value"`     // 数据值
	Timestamp int64  `json:"timestamp"` // 上报时间戳（秒）
	Signature string `json:"signature"` // 设备对数据的 ECDSA 签名，hex(r||s)
}

// SmartContract 链码主合约
type SmartContract struct {
	contractapi.Contract
}

// ================= 设备注册与生命周期 =================

// RegisterDevice 注册设备，状态置为待激活(0)
func (s *SmartContract) RegisterDevice(ctx contractapi.TransactionContextInterface, deviceID, owner, manufacturer, model, publicKey string) error {
	if deviceID == "" {
		return fmt.Errorf("deviceID must not be empty")
	}
	// 公钥必须是合法的 ECDSA 公钥，注册即绑定，供后续上报验签使用
	if _, err := parsePublicKey(publicKey); err != nil {
		return err
	}
	exists, err := s.deviceExists(ctx, deviceID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("device %s already exists", deviceID)
	}

	caller, err := getCallerID(ctx)
	if err != nil {
		return err
	}
	// owner 为空时默认为调用方；显式指定时必须等于调用方，避免产生无人能控制的“孤儿设备”
	if owner == "" {
		owner = caller
	} else if owner != caller {
		return fmt.Errorf("owner must be empty or equal to the caller identity")
	}

	now, err := nowUnix(ctx)
	if err != nil {
		return err
	}

	device := Device{
		DeviceID:     deviceID,
		Owner:        owner,
		Manufacturer: manufacturer,
		Model:        model,
		PublicKey:    publicKey,
		Status:       StatusPending,
		RegisteredAt: now,
		RevokedAt:    0,
	}
	return s.putDevice(ctx, device)
}

// ActivateDevice 激活设备（0 → 1），仅所有者可调用
func (s *SmartContract) ActivateDevice(ctx contractapi.TransactionContextInterface, deviceID string) error {
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if err := requireOwner(ctx, device); err != nil {
		return err
	}
	if device.Status != StatusPending {
		return fmt.Errorf("device %s status %d cannot be activated", deviceID, device.Status)
	}
	device.Status = StatusNormal
	return s.putDevice(ctx, *device)
}

// RevokeDevice 吊销设备（→ 2），仅所有者可调用
func (s *SmartContract) RevokeDevice(ctx contractapi.TransactionContextInterface, deviceID string) error {
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if err := requireOwner(ctx, device); err != nil {
		return err
	}
	if device.Status == StatusRevoked {
		return fmt.Errorf("device %s already revoked", deviceID)
	}
	now, err := nowUnix(ctx)
	if err != nil {
		return err
	}
	device.Status = StatusRevoked
	device.RevokedAt = now
	return s.putDevice(ctx, *device)
}

// ================= 授权管理 =================

// AuthorizeAccess 授权某数据消费方读取设备数据，仅所有者可调用
func (s *SmartContract) AuthorizeAccess(ctx contractapi.TransactionContextInterface, deviceID, consumer string) error {
	if consumer == "" {
		return fmt.Errorf("consumer must not be empty")
	}
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if err := requireOwner(ctx, device); err != nil {
		return err
	}
	if device.Status != StatusNormal {
		return fmt.Errorf("device %s is not active", deviceID)
	}
	now, err := nowUnix(ctx)
	if err != nil {
		return err
	}
	auth := Authorization{
		DeviceID:  deviceID,
		Consumer:  consumer,
		GrantedAt: now,
		RevokedAt: 0,
		Active:    true,
	}
	return s.putAuthorization(ctx, auth)
}

// RevokeAccess 撤销某消费方的数据访问授权，仅所有者可调用
func (s *SmartContract) RevokeAccess(ctx contractapi.TransactionContextInterface, deviceID, consumer string) error {
	if consumer == "" {
		return fmt.Errorf("consumer must not be empty")
	}
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if err := requireOwner(ctx, device); err != nil {
		return err
	}
	auth, err := s.getAuthorization(ctx, deviceID, consumer)
	if err != nil {
		return err
	}
	if auth == nil {
		return fmt.Errorf("no authorization for device %s consumer %s", deviceID, consumer)
	}
	if !auth.Active {
		return fmt.Errorf("authorization for device %s consumer %s already revoked", deviceID, consumer)
	}
	now, err := nowUnix(ctx)
	if err != nil {
		return err
	}
	auth.Active = false
	auth.RevokedAt = now
	return s.putAuthorization(ctx, *auth)
}

// ================= 数据上报 =================

// ReportData 设备直连上报数据；仅设备所有者或被授权消费方可调用，且数据须带设备私钥签名
func (s *SmartContract) ReportData(ctx contractapi.TransactionContextInterface, deviceID, dataType, value string, timestamp int64, signature string) error {
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if device.Status != StatusNormal {
		return fmt.Errorf("device %s is not active (status=%d)", deviceID, device.Status)
	}
	if dataType == "" || value == "" {
		return fmt.Errorf("dataType and value must not be empty")
	}
	if timestamp < 0 {
		return fmt.Errorf("timestamp must not be negative")
	}

	caller, err := getCallerID(ctx)
	if err != nil {
		return err
	}
	authorized, err := s.isAuthorized(ctx, device, caller)
	if err != nil {
		return err
	}
	if !authorized {
		return fmt.Errorf("caller is not owner or authorized consumer of device %s", deviceID)
	}

	// 验签：用设备注册的公钥校验设备对“原始上报字段”的签名（验签用原始 timestamp，替换 0 之前）
	if err := verifyReportSignature(device.PublicKey, signature, deviceID, dataType, value, timestamp); err != nil {
		return err
	}

	if timestamp == 0 {
		now, err := nowUnix(ctx)
		if err != nil {
			return err
		}
		timestamp = now
	}
	recordID := fmt.Sprintf("%d", timestamp)
	key := dataKey(deviceID, recordID)
	for i := 1; ; i++ {
		exists, err := s.recordExists(ctx, key)
		if err != nil {
			return err
		}
		if !exists {
			break
		}
		recordID = fmt.Sprintf("%d-%d", timestamp, i)
		key = dataKey(deviceID, recordID)
	}

	record := DataRecord{
		RecordID:  recordID,
		DeviceID:  deviceID,
		DataType:  dataType,
		Value:     value,
		Timestamp: timestamp,
		Signature: signature,
	}
	return s.putData(ctx, record)
}

// ================= 查询 =================

// GetDevice 查询单个设备
func (s *SmartContract) GetDevice(ctx contractapi.TransactionContextInterface, deviceID string) (*Device, error) {
	return s.getDevice(ctx, deviceID)
}

// GetAllDevices 查询全部设备
func (s *SmartContract) GetAllDevices(ctx contractapi.TransactionContextInterface) ([]*Device, error) {
	iterator, err := ctx.GetStub().GetStateByRange("device-", "device-￿")
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	var devices []*Device
	for iterator.HasNext() {
		kv, err := iterator.Next()
		if err != nil {
			return nil, err
		}
		var device Device
		if err := json.Unmarshal(kv.Value, &device); err != nil {
			return nil, err
		}
		devices = append(devices, &device)
	}
	return devices, nil
}

// GetAuthorization 查询某设备对某消费方的授权，仅所有者可调用
func (s *SmartContract) GetAuthorization(ctx contractapi.TransactionContextInterface, deviceID, consumer string) (*Authorization, error) {
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if err := requireOwner(ctx, device); err != nil {
		return nil, err
	}
	auth, err := s.getAuthorization(ctx, deviceID, consumer)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, fmt.Errorf("no authorization for device %s consumer %s", deviceID, consumer)
	}
	return auth, nil
}

// GetAllAuthorizations 查询某设备的全部授权，仅所有者可调用
func (s *SmartContract) GetAllAuthorizations(ctx contractapi.TransactionContextInterface, deviceID string) ([]*Authorization, error) {
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if err := requireOwner(ctx, device); err != nil {
		return nil, err
	}
	start := "auth-" + deviceID + "-"
	iterator, err := ctx.GetStub().GetStateByRange(start, start+"￿")
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	var auths []*Authorization
	for iterator.HasNext() {
		kv, err := iterator.Next()
		if err != nil {
			return nil, err
		}
		var auth Authorization
		if err := json.Unmarshal(kv.Value, &auth); err != nil {
			return nil, err
		}
		auths = append(auths, &auth)
	}
	return auths, nil
}

// GetDeviceData 查询某设备上报的全部数据记录，仅设备所有者或被授权消费方可调用
func (s *SmartContract) GetDeviceData(ctx contractapi.TransactionContextInterface, deviceID string) ([]*DataRecord, error) {
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	caller, err := getCallerID(ctx)
	if err != nil {
		return nil, err
	}
	authorized, err := s.isAuthorized(ctx, device, caller)
	if err != nil {
		return nil, err
	}
	if !authorized {
		return nil, fmt.Errorf("caller is not owner or authorized consumer of device %s", deviceID)
	}

	start := "data-" + deviceID + "-"
	iterator, err := ctx.GetStub().GetStateByRange(start, start+"￿")
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	var records []*DataRecord
	for iterator.HasNext() {
		kv, err := iterator.Next()
		if err != nil {
			return nil, err
		}
		var record DataRecord
		if err := json.Unmarshal(kv.Value, &record); err != nil {
			return nil, err
		}
		records = append(records, &record)
	}
	return records, nil
}

// ================= 内部工具函数 =================

// getCallerID 返回当前交易提交方的身份标识
func getCallerID(ctx contractapi.TransactionContextInterface) (string, error) {
	id, err := ctx.GetClientIdentity().GetID()
	if err != nil {
		return "", fmt.Errorf("failed to get client identity: %v", err)
	}
	return id, nil
}

// requireOwner 校验当前调用方是否为设备所有者
func requireOwner(ctx contractapi.TransactionContextInterface, device *Device) error {
	caller, err := getCallerID(ctx)
	if err != nil {
		return err
	}
	if caller != device.Owner {
		return fmt.Errorf("caller is not the owner of device %s", device.DeviceID)
	}
	return nil
}

// isAuthorized 判断 caller 是否为设备所有者或仍有效的被授权消费方
func (s *SmartContract) isAuthorized(ctx contractapi.TransactionContextInterface, device *Device, caller string) (bool, error) {
	if caller == device.Owner {
		return true, nil
	}
	auth, err := s.getAuthorization(ctx, device.DeviceID, caller)
	if err != nil {
		return false, err
	}
	if auth == nil {
		return false, nil
	}
	return auth.Active, nil
}

// nowUnix 返回当前交易的 proposal 时间戳（秒），各背书节点一致，保证链码确定性
func nowUnix(ctx contractapi.TransactionContextInterface) (int64, error) {
	ts, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return 0, fmt.Errorf("failed to get tx timestamp: %v", err)
	}
	return ts.Seconds, nil
}

// parsePublicKey 解析 hex 编码的 PKIX/DER ECDSA 公钥
func parsePublicKey(pubKeyHex string) (*ecdsa.PublicKey, error) {
	pubDER, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return nil, fmt.Errorf("publicKey is not valid hex: %v", err)
	}
	key, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		return nil, fmt.Errorf("publicKey is not a valid public key: %v", err)
	}
	ek, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("publicKey is not an ECDSA public key")
	}
	return ek, nil
}

// verifyReportSignature 用设备公钥校验对上报数据（原始字段）的 ECDSA 签名
// 签名格式：hex(r||s)，r、s 各 32 字节（P-256）
func verifyReportSignature(pubKeyHex, sigHex, deviceID, dataType, value string, rawTimestamp int64) error {
	if pubKeyHex == "" || sigHex == "" {
		return fmt.Errorf("publicKey and signature are required")
	}
	pub, err := parsePublicKey(pubKeyHex)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("signature is not valid hex: %v", err)
	}
	if len(sig) != 64 {
		return fmt.Errorf("signature length %d, want 64 bytes (r||s)", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	msg := strings.Join([]string{deviceID, dataType, value, strconv.FormatInt(rawTimestamp, 10)}, "|")
	digest := sha256.Sum256([]byte(msg))
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return fmt.Errorf("signature verification failed for device %s", deviceID)
	}
	return nil
}

func (s *SmartContract) deviceExists(ctx contractapi.TransactionContextInterface, deviceID string) (bool, error) {
	v, err := ctx.GetStub().GetState(deviceKey(deviceID))
	if err != nil {
		return false, fmt.Errorf("failed to read device %s: %v", deviceID, err)
	}
	return v != nil, nil
}

func (s *SmartContract) getDevice(ctx contractapi.TransactionContextInterface, deviceID string) (*Device, error) {
	v, err := ctx.GetStub().GetState(deviceKey(deviceID))
	if err != nil {
		return nil, fmt.Errorf("failed to read device %s: %v", deviceID, err)
	}
	if v == nil {
		return nil, fmt.Errorf("device %s does not exist", deviceID)
	}
	var device Device
	if err := json.Unmarshal(v, &device); err != nil {
		return nil, fmt.Errorf("failed to unmarshal device %s: %v", deviceID, err)
	}
	return &device, nil
}

func (s *SmartContract) putDevice(ctx contractapi.TransactionContextInterface, device Device) error {
	v, err := json.Marshal(device)
	if err != nil {
		return fmt.Errorf("failed to marshal device: %v", err)
	}
	return ctx.GetStub().PutState(deviceKey(device.DeviceID), v)
}

func (s *SmartContract) getAuthorization(ctx contractapi.TransactionContextInterface, deviceID, consumer string) (*Authorization, error) {
	v, err := ctx.GetStub().GetState(authKey(deviceID, consumer))
	if err != nil {
		return nil, fmt.Errorf("failed to read authorization: %v", err)
	}
	if v == nil {
		return nil, nil
	}
	var auth Authorization
	if err := json.Unmarshal(v, &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}

func (s *SmartContract) putAuthorization(ctx contractapi.TransactionContextInterface, auth Authorization) error {
	v, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	return ctx.GetStub().PutState(authKey(auth.DeviceID, auth.Consumer), v)
}

func (s *SmartContract) putData(ctx contractapi.TransactionContextInterface, record DataRecord) error {
	v, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return ctx.GetStub().PutState(dataKey(record.DeviceID, record.RecordID), v)
}

func (s *SmartContract) recordExists(ctx contractapi.TransactionContextInterface, key string) (bool, error) {
	v, err := ctx.GetStub().GetState(key)
	if err != nil {
		return false, err
	}
	return v != nil, nil
}

// 状态键规则：设备用 device-、授权用 auth-<deviceID>-、数据用 data-<deviceID>-
func deviceKey(deviceID string) string { return "device-" + deviceID }
func authKey(deviceID, consumer string) string {
	return "auth-" + deviceID + "-" + consumer
}
func dataKey(deviceID, recordID string) string { return "data-" + deviceID + "-" + recordID }
