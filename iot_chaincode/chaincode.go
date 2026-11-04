package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

// ============ 数据结构（对齐数据字典） ============

type User struct {
	Key          string `json:"key"`
	Username     string `json:"username"`
	Phone        string `json:"phone"`
	PasswordHash string `json:"passwordHash"`
	Role         string `json:"role"`
	CreateTime   string `json:"createTime"`
}

type Device struct {
	DeviceID        string `json:"deviceID"`
	DeviceName      string `json:"deviceName"`
	DeviceType      string `json:"deviceType"`
	Owner           string `json:"owner"`
	GroupID         string `json:"groupID"`
	Status          string `json:"status"`
	Manufacturer    string `json:"manufacturer"`
	AuthorizedUntil string `json:"authorizedUntil"` // 授权过期时间 RFC3339，过期后禁止上链
}

type Group struct {
	GroupID    string   `json:"groupID"`
	GroupName  string   `json:"groupName"`
	OwnerOrg   string   `json:"ownerOrg"`
	DeviceList []string `json:"deviceList"`
	Desc       string   `json:"description"`
}

type DataHash struct {
	HashValue string `json:"hashValue"`
	DeviceID  string `json:"deviceID"`
	Timestamp string `json:"timestamp"`
	TxID      string `json:"txID"`
}

type Operation struct {
	TxID      string `json:"txID"`
	OpType    string `json:"opType"`
	Detail    string `json:"detail"`
	CallerMSP string `json:"callerMSP"`
	Time      string `json:"time"`
}

// KV 范围查询单元（键 + 原始值）
type KV struct {
	Key   string
	Value []byte
}

type SmartContract struct {
	contractapi.Contract
}

// ============ 辅助方法 ============

// txNow 取交易时间戳（取自提案 ChannelHeader，所有背书节点取值一致，保证链码确定性）
// 不能使用 time.Now()：多个背书节点执行时刻不同会导致读写集不一致、背书失败
func (s *SmartContract) txNow(ctx contractapi.TransactionContextInterface) (time.Time, error) {
	ts, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(ts.Seconds, int64(ts.Nanos)), nil
}

// getCallerMSP 从客户端证书拿到调用方组织 MSP ID（权限判定依据）
func (s *SmartContract) getCallerMSP(ctx contractapi.TransactionContextInterface) (string, error) {
	msp, err := ctx.GetClientIdentity().GetMSPID()
	if err != nil {
		return "", fmt.Errorf("获取调用者身份失败: %v", err)
	}
	return msp, nil
}

// requireAdmin 校验调用方是否为平台管理组织 Org1MSP，否则拒绝。
// 修复漏洞：原实现 AddUser/AddDevice/AddGroup/AddDeviceToGroup/UploadDataHash 均无写权限校验，
// 任何组织（如 Org2MSP）都能越权写账本，甚至注册 role=admin 用户（提权）。
func (s *SmartContract) requireAdmin(ctx contractapi.TransactionContextInterface) error {
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return err
	}
	if msp != "Org1MSP" {
		return fmt.Errorf("访问被拒绝：仅 Org1MSP 平台管理组织可执行该写操作，当前调用方 MSP=%s", msp)
	}
	return nil
}

// recordOperation 记录操作审计：每笔成功写操作写入 OPERATION_<txID>，并对 OPSTAT_<opType> 计数。
// 支撑 FR8 交易审计统计（QueryOperations / CountOperations）。
func (s *SmartContract) recordOperation(ctx contractapi.TransactionContextInterface, opType, detail string) error {
	stub := ctx.GetStub()
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return err
	}
	now, err := s.txNow(ctx)
	if err != nil {
		return err
	}
	op := Operation{
		TxID:      stub.GetTxID(),
		OpType:    opType,
		Detail:    detail,
		CallerMSP: msp,
		Time:      now.Format(time.RFC3339),
	}
	data, _ := json.Marshal(op)
	if err := stub.PutState("OPERATION_"+stub.GetTxID(), data); err != nil {
		return err
	}
	statKey := "OPSTAT_" + opType
	statBytes, err := stub.GetState(statKey)
	if err != nil {
		return err
	}
	count := 0
	if statBytes != nil {
		count, _ = strconv.Atoi(string(statBytes))
	}
	return stub.PutState(statKey, []byte(strconv.Itoa(count+1)))
}

// rangeByPrefix 按键前缀范围查询，返回键值列表
func (s *SmartContract) rangeByPrefix(ctx contractapi.TransactionContextInterface, prefix string) ([]KV, error) {
	stub := ctx.GetStub()
	it, err := stub.GetStateByRange(prefix, prefix+string(utf8.MaxRune))
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var out []KV
	for it.HasNext() {
		item, err := it.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, KV{Key: item.Key, Value: item.Value})
	}
	return out, nil
}

// ============ 1. 注册类 ============

func (s *SmartContract) AddUser(ctx contractapi.TransactionContextInterface, key, username, phone, passwordHash, role string) error {
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}
	stub := ctx.GetStub()
	existing, err := stub.GetState("USER_" + key)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("用户 %s 已存在", key)
	}
	now, err := s.txNow(ctx)
	if err != nil {
		return err
	}
	user := User{
		Key:          key,
		Username:     username,
		Phone:        phone,
		PasswordHash: passwordHash,
		Role:         role,
		CreateTime:   now.Format(time.RFC3339),
	}
	data, _ := json.Marshal(user)
	if err := stub.PutState("USER_"+key, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "AddUser", "注册用户 "+key+"(role="+role+")")
}

func (s *SmartContract) AddDevice(ctx contractapi.TransactionContextInterface, deviceID, name, devType, owner, manufacturer, authorizedUntil string) error {
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}
	stub := ctx.GetStub()
	existing, err := stub.GetState("DEVICE_" + deviceID)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("设备 %s 已注册", deviceID)
	}
	ownerBytes, err := stub.GetState("USER_" + owner)
	if err != nil {
		return err
	}
	if ownerBytes == nil {
		return fmt.Errorf("归属用户 %s 不存在", owner)
	}
	// 校验授权时间格式
	if _, err := time.Parse(time.RFC3339, authorizedUntil); err != nil {
		return fmt.Errorf("authorizedUntil 格式错误(需 RFC3339): %v", err)
	}
	device := Device{
		DeviceID:        deviceID,
		DeviceName:      name,
		DeviceType:      devType,
		Owner:           owner,
		GroupID:         "",
		Status:          "active",
		Manufacturer:    manufacturer,
		AuthorizedUntil: authorizedUntil,
	}
	data, _ := json.Marshal(device)
	if err := stub.PutState("DEVICE_"+deviceID, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "AddDevice", "注册设备 "+deviceID)
}

// ============ 2. 授权管理类 ============

func (s *SmartContract) AddGroup(ctx contractapi.TransactionContextInterface, groupID, groupName, ownerOrg, desc string) error {
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}
	stub := ctx.GetStub()
	existing, _ := stub.GetState("GROUP_" + groupID)
	if existing != nil {
		return fmt.Errorf("分组 %s 已存在", groupID)
	}
	group := Group{
		GroupID:    groupID,
		GroupName:  groupName,
		OwnerOrg:   ownerOrg,
		DeviceList: []string{},
		Desc:       desc,
	}
	data, _ := json.Marshal(group)
	if err := stub.PutState("GROUP_"+groupID, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "AddGroup", "创建分组 "+groupID)
}

func (s *SmartContract) AddDeviceToGroup(ctx contractapi.TransactionContextInterface, groupID, deviceID string) error {
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}
	stub := ctx.GetStub()
	groupBytes, err := stub.GetState("GROUP_" + groupID)
	if err != nil {
		return err
	}
	if groupBytes == nil {
		return fmt.Errorf("分组 %s 不存在", groupID)
	}
	deviceBytes, err := stub.GetState("DEVICE_" + deviceID)
	if err != nil {
		return err
	}
	if deviceBytes == nil {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	var group Group
	_ = json.Unmarshal(groupBytes, &group)
	var device Device
	_ = json.Unmarshal(deviceBytes, &device)
	for _, d := range group.DeviceList {
		if d == deviceID {
			return nil
		}
	}
	group.DeviceList = append(group.DeviceList, deviceID)
	device.GroupID = groupID
	groupData, _ := json.Marshal(group)
	deviceData, _ := json.Marshal(device)
	if err := stub.PutState("GROUP_"+groupID, groupData); err != nil {
		return err
	}
	if err := stub.PutState("DEVICE_"+deviceID, deviceData); err != nil {
		return err
	}
	return s.recordOperation(ctx, "AddDeviceToGroup", "设备 "+deviceID+" 加入分组 "+groupID)
}

// ExpireAuthorization 管理员修改设备授权过期时间（仅 Org1MSP 管理员可调用）
// 用于演示"过期授权操作被拒绝"：把 until 改成过去时间后，设备将无法再上链
func (s *SmartContract) ExpireAuthorization(ctx contractapi.TransactionContextInterface, deviceID, until string) error {
	stub := ctx.GetStub()
	// 权限校验：只有 Org1MSP（平台管理员组织）能修改授权
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return err
	}
	if msp != "Org1MSP" {
		return fmt.Errorf("权限不足：只有 Org1MSP 管理员可修改授权，当前调用方 MSP=%s", msp)
	}
	deviceBytes, err := stub.GetState("DEVICE_" + deviceID)
	if err != nil {
		return err
	}
	if deviceBytes == nil {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	if _, err := time.Parse(time.RFC3339, until); err != nil {
		return fmt.Errorf("until 格式错误(需 RFC3339): %v", err)
	}
	var device Device
	_ = json.Unmarshal(deviceBytes, &device)
	device.AuthorizedUntil = until
	data, _ := json.Marshal(device)
	if err := stub.PutState("DEVICE_"+deviceID, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "ExpireAuthorization", "设备 "+deviceID+" 授权期改至 "+until)
}

// ============ 3. 数据上链类 ============

func (s *SmartContract) UploadDataHash(ctx contractapi.TransactionContextInterface, deviceID, timestamp, hashValue string) error {
	stub := ctx.GetStub()
	deviceBytes, err := stub.GetState("DEVICE_" + deviceID)
	if err != nil {
		return err
	}
	// 【防护 1：拦截伪造设备上链】
	if deviceBytes == nil {
		return fmt.Errorf("访问被拒绝：设备 %s 未注册，伪造设备上链请求已被拦截", deviceID)
	}
	// 【防护 4：拦截越权上链】上链方必须是平台管理组织 Org1MSP（修复：原实现任何组织都能替他人设备上链）
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}

	// 【防护 5：存证不可覆盖】同一 (deviceID, timestamp) 只允许写入一次，防止篡改已存证哈希
	key := "DATAHASH_" + deviceID + "_" + timestamp
	existingHash, err := stub.GetState(key)
	if err != nil {
		return err
	}
	if existingHash != nil {
		return fmt.Errorf("访问被拒绝：设备 %s 在时间戳 %s 已有存证记录，存证不可覆盖（防篡改）", deviceID, timestamp)
	}

	var device Device
	_ = json.Unmarshal(deviceBytes, &device)

	// 【防护 2：拦截过期授权操作】
	expireTime, err := time.Parse(time.RFC3339, device.AuthorizedUntil)
	if err != nil {
		return fmt.Errorf("设备授权时间字段异常: %v", err)
	}
	now, err := s.txNow(ctx)
	if err != nil {
		return err
	}
	if now.After(expireTime) {
		return fmt.Errorf("访问被拒绝：设备 %s 授权已于 %s 过期，过期授权操作已被拦截", deviceID, device.AuthorizedUntil)
	}

	record := DataHash{
		HashValue: hashValue,
		DeviceID:  deviceID,
		Timestamp: timestamp,
		TxID:      stub.GetTxID(),
	}
	data, _ := json.Marshal(record)
	if err := stub.PutState(key, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "UploadDataHash", "设备 "+deviceID+" 存证 "+hashValue)
}

// ============ 4. 查询/追溯类 ============

func (s *SmartContract) QueryDevice(ctx contractapi.TransactionContextInterface, deviceID string) (*Device, error) {
	stub := ctx.GetStub()
	bytes, err := stub.GetState("DEVICE_" + deviceID)
	if err != nil {
		return nil, err
	}
	if bytes == nil {
		return nil, fmt.Errorf("设备 %s 不存在", deviceID)
	}
	var device Device
	_ = json.Unmarshal(bytes, &device)
	return &device, nil
}

// QueryAllDevices 【防护 3：拦截越权查询】只有 Org1MSP（审计方）能全量遍历，其他组织拒绝
func (s *SmartContract) QueryAllDevices(ctx contractapi.TransactionContextInterface) ([]*Device, error) {
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != "Org1MSP" {
		return nil, fmt.Errorf("访问被拒绝：越权查询，只有 Org1MSP 审计方可全量遍历设备，当前调用方 MSP=%s", msp)
	}
	stub := ctx.GetStub()
	resultsIterator, err := stub.GetStateByRange("DEVICE_", "DEVICE_"+string(utf8.MaxRune))
	if err != nil {
		return nil, err
	}
	defer resultsIterator.Close()
	var devices []*Device
	for resultsIterator.HasNext() {
		item, err := resultsIterator.Next()
		if err != nil {
			return nil, err
		}
		var d Device
		_ = json.Unmarshal(item.Value, &d)
		devices = append(devices, &d)
	}
	return devices, nil
}

func (s *SmartContract) QueryDataHash(ctx contractapi.TransactionContextInterface, deviceID, timestamp string) (*DataHash, error) {
	stub := ctx.GetStub()
	key := "DATAHASH_" + deviceID + "_" + timestamp
	bytes, err := stub.GetState(key)
	if err != nil {
		return nil, err
	}
	if bytes == nil {
		return nil, fmt.Errorf("未找到 device=%s timestamp=%s 的存证记录", deviceID, timestamp)
	}
	var record DataHash
	_ = json.Unmarshal(bytes, &record)
	return &record, nil
}

// ============ 5. 用户信息维护（FR1） ============

// UpdateUser 更新用户非敏感字段（用户名/手机号/角色），密码哈希与创建时间保持不变
func (s *SmartContract) UpdateUser(ctx contractapi.TransactionContextInterface, key, username, phone, role string) error {
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}
	stub := ctx.GetStub()
	bytes, err := stub.GetState("USER_" + key)
	if err != nil {
		return err
	}
	if bytes == nil {
		return fmt.Errorf("用户 %s 不存在", key)
	}
	var user User
	_ = json.Unmarshal(bytes, &user)
	user.Username = username
	user.Phone = phone
	user.Role = role
	data, _ := json.Marshal(user)
	if err := stub.PutState("USER_"+key, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "UpdateUser", "更新用户 "+key)
}

// QueryUser 查询单个用户（联盟内开放）
func (s *SmartContract) QueryUser(ctx contractapi.TransactionContextInterface, key string) (*User, error) {
	stub := ctx.GetStub()
	bytes, err := stub.GetState("USER_" + key)
	if err != nil {
		return nil, err
	}
	if bytes == nil {
		return nil, fmt.Errorf("用户 %s 不存在", key)
	}
	var user User
	_ = json.Unmarshal(bytes, &user)
	return &user, nil
}

// QueryAllUsers 全量遍历用户，仅 Org1MSP 审计方可调用（对齐防护 3）
func (s *SmartContract) QueryAllUsers(ctx contractapi.TransactionContextInterface) ([]*User, error) {
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != "Org1MSP" {
		return nil, fmt.Errorf("访问被拒绝：越权查询，只有 Org1MSP 审计方可全量遍历用户，当前调用方 MSP=%s", msp)
	}
	items, err := s.rangeByPrefix(ctx, "USER_")
	if err != nil {
		return nil, err
	}
	var users []*User
	for _, kv := range items {
		var u User
		_ = json.Unmarshal(kv.Value, &u)
		users = append(users, &u)
	}
	return users, nil
}

// ============ 6. 设备台账维护（FR2） ============

// ModifyDevice 管理员修改设备台账（名称/厂商/授权有效期；传空保持原值）
func (s *SmartContract) ModifyDevice(ctx contractapi.TransactionContextInterface, deviceID, name, manufacturer, authorizedUntil string) error {
	if err := s.requireAdmin(ctx); err != nil {
		return err
	}
	stub := ctx.GetStub()
	deviceBytes, err := stub.GetState("DEVICE_" + deviceID)
	if err != nil {
		return err
	}
	if deviceBytes == nil {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	var device Device
	_ = json.Unmarshal(deviceBytes, &device)
	if name != "" {
		device.DeviceName = name
	}
	if manufacturer != "" {
		device.Manufacturer = manufacturer
	}
	if authorizedUntil != "" {
		if _, err := time.Parse(time.RFC3339, authorizedUntil); err != nil {
			return fmt.Errorf("authorizedUntil 格式错误(需 RFC3339): %v", err)
		}
		device.AuthorizedUntil = authorizedUntil
	}
	data, _ := json.Marshal(device)
	if err := stub.PutState("DEVICE_"+deviceID, data); err != nil {
		return err
	}
	return s.recordOperation(ctx, "ModifyDevice", "修改设备 "+deviceID)
}

// ============ 7. 分组查询（FR3） ============

// QueryGroup 查询单个分组（联盟内开放）
func (s *SmartContract) QueryGroup(ctx contractapi.TransactionContextInterface, groupID string) (*Group, error) {
	stub := ctx.GetStub()
	bytes, err := stub.GetState("GROUP_" + groupID)
	if err != nil {
		return nil, err
	}
	if bytes == nil {
		return nil, fmt.Errorf("分组 %s 不存在", groupID)
	}
	var group Group
	_ = json.Unmarshal(bytes, &group)
	return &group, nil
}

// QueryAllGroups 全量遍历分组，仅 Org1MSP 审计方可调用（对齐防护 3）
func (s *SmartContract) QueryAllGroups(ctx contractapi.TransactionContextInterface) ([]*Group, error) {
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != "Org1MSP" {
		return nil, fmt.Errorf("访问被拒绝：越权查询，只有 Org1MSP 审计方可全量遍历分组，当前调用方 MSP=%s", msp)
	}
	items, err := s.rangeByPrefix(ctx, "GROUP_")
	if err != nil {
		return nil, err
	}
	var groups []*Group
	for _, kv := range items {
		var g Group
		_ = json.Unmarshal(kv.Value, &g)
		groups = append(groups, &g)
	}
	return groups, nil
}

// ============ 8. 操作审计（FR8） ============

// QueryOperations 查询操作审计记录，仅 Org1MSP 审计方可调用；opType 传 ALL 返回全部
func (s *SmartContract) QueryOperations(ctx contractapi.TransactionContextInterface, opType string) ([]*Operation, error) {
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != "Org1MSP" {
		return nil, fmt.Errorf("访问被拒绝：越权查询，只有 Org1MSP 审计方可查询操作审计，当前调用方 MSP=%s", msp)
	}
	items, err := s.rangeByPrefix(ctx, "OPERATION_")
	if err != nil {
		return nil, err
	}
	var ops []*Operation
	for _, kv := range items {
		var op Operation
		_ = json.Unmarshal(kv.Value, &op)
		if opType == "ALL" || op.OpType == opType {
			ops = append(ops, &op)
		}
	}
	return ops, nil
}

// CountOperations 统计各操作类型的调用次数，仅 Org1MSP 审计方可调用
func (s *SmartContract) CountOperations(ctx contractapi.TransactionContextInterface) (map[string]int, error) {
	msp, err := s.getCallerMSP(ctx)
	if err != nil {
		return nil, err
	}
	if msp != "Org1MSP" {
		return nil, fmt.Errorf("访问被拒绝：越权查询，只有 Org1MSP 审计方可统计操作调用，当前调用方 MSP=%s", msp)
	}
	items, err := s.rangeByPrefix(ctx, "OPSTAT_")
	if err != nil {
		return nil, err
	}
	stats := map[string]int{}
	for _, kv := range items {
		if strings.HasPrefix(kv.Key, "OPSTAT_") {
			n, _ := strconv.Atoi(string(kv.Value))
			stats[strings.TrimPrefix(kv.Key, "OPSTAT_")] = n
		}
	}
	return stats, nil
}

// ============ 入口 ============

func main() {
	chaincode, err := contractapi.NewChaincode(&SmartContract{})
	if err != nil {
		panic(err)
	}
	if err := chaincode.Start(); err != nil {
		panic(err)
	}
}
