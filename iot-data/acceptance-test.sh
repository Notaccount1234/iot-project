#!/bin/bash
# =============================================================
# iot-data 链码 v2.0 · 验收 + 回归测试
# 覆盖：
#   ① 正常(注册→授权→上链→查询)  ② 重复注册  ③ 未授权上链
#   ④ 越权上链  ⑤ 过期(已撤销)授权上链  ⑥ 未授权读取(回归)  ⑦ 伪造签名上报(回归)
# 前置：test-network 已启动、mychannel 已创建、iot-data 已升级到 v2.0
# 运行：cd /home/ljx/fabric-samples/iot-data && bash acceptance-test.sh
# 证据：./acceptance-evidence/{normal.log, rejected.log}
# =============================================================
set -o pipefail
cd /home/ljx/fabric-samples/iot-data || exit 1
source ./env.sh        # Org1 Admin 身份（设备 owner）

CH=iot-data
ORDERER="localhost:7050"
ORDERER_HOST="orderer.example.com"
ENDORSE="--peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_TLS --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_TLS"
WAIT="--waitForEvent --waitForEventTimeout 30s"

MSP1=/home/ljx/fabric-samples/test-network/organizations/peerOrganizations/org1.example.com
MSP2=/home/ljx/fabric-samples/test-network/organizations/peerOrganizations/org2.example.com

as_owner() {     # 设备所有者 = Org1 Admin
  export CORE_PEER_LOCALMSPID=Org1MSP
  export CORE_PEER_TLS_ENABLED=true
  export CORE_PEER_TLS_ROOTCERT_FILE=$MSP1/peers/peer0.org1.example.com/tls/ca.crt
  export CORE_PEER_MSPCONFIGPATH=$MSP1/users/Admin@org1.example.com/msp
  export CORE_PEER_ADDRESS=localhost:7051
}
as_consumer1() { # 被授权消费方 = Org1 User1
  export CORE_PEER_LOCALMSPID=Org1MSP
  export CORE_PEER_TLS_ENABLED=true
  export CORE_PEER_TLS_ROOTCERT_FILE=$MSP1/peers/peer0.org1.example.com/tls/ca.crt
  export CORE_PEER_MSPCONFIGPATH=$MSP1/users/User1@org1.example.com/msp
  export CORE_PEER_ADDRESS=localhost:7051
}
as_consumer2() { # 从未授权的消费方 = Org2 User1
  export CORE_PEER_LOCALMSPID=Org2MSP
  export CORE_PEER_TLS_ENABLED=true
  export CORE_PEER_TLS_ROOTCERT_FILE=$MSP2/peers/peer0.org2.example.com/tls/ca.crt
  export CORE_PEER_MSPCONFIGPATH=$MSP2/users/User1@org2.example.com/msp
  export CORE_PEER_ADDRESS=localhost:9051
}

invoke() {
  peer chaincode invoke -o "$ORDERER" --ordererTLSHostnameOverride "$ORDERER_HOST" --tls --cafile "$ORDERER_CA" \
    -C mychannel -n "$CH" $ENDORSE $WAIT -c "$1"
}
query() {
  peer chaincode query -C mychannel -n "$CH" -c "$1"
}

# 两个消费方的链码侧 caller ID（由各自证书固定生成，可用 GetDevice.owner 反查）
C1_ID='eDUwOTo6Q049VXNlcjFAb3JnMS5leGFtcGxlLmNvbSxPVT1jbGllbnQsTD1TYW4gRnJhbmNpc2NvLFNUPUNhbGlmb3JuaWEsQz1VUzo6Q049Y2Eub3JnMS5leGFtcGxlLmNvbSxPPW9yZzEuZXhhbXBsZS5jb20sTD1TYW4gRnJhbmNpc2NvLFNUPUNhbGlmb3JuaWEsQz1VUw=='
C2_ID='eDUwOTo6Q049VXNlcjFAb3JnMi5leGFtcGxlLmNvbSxPVT1jbGllbnQsTD1TYW4gRnJhbmNpc2NvLFNUPUNhbGlmb3JuaWEsQz1VUzo6Q049Y2Eub3JnMi5leGFtcGxlLmNvbSxPPW9yZzIuZXhhbXBsZS5jb20sTD1TYW4gRnJhbmNpc2NvLFNUPUNhbGlmb3JuaWEsQz1VUw=='

EVID=acceptance-evidence
mkdir -p "$EVID"
: > "$EVID/normal.log"
: > "$EVID/rejected.log"
RUN=$(date +%s)
DA="devA-$RUN"
DB="devB-$RUN"

# 生成测试密钥，并对“成功上报”那条 (DA,temperature,25.5,0) 预先签名
eval "$(go run genkeys.go \"$DA\" temperature 25.5 0 2>/dev/null)"
if [ -z "$DEV_PUBKEY" ] || [ -z "$DEV_SIG" ]; then
  echo "密钥/签名生成失败，请检查 go 环境（go run genkeys.go）" ; exit 1
fi

rejected() {
  local title="$1" payload="$2"
  echo "" | tee -a "$EVID/rejected.log"
  echo "========== $title ==========" | tee -a "$EVID/rejected.log"
  invoke "$payload" 2>&1 | tee -a "$EVID/rejected.log"
  echo "---- 退出码 = ${PIPESTATUS[0]}（非 0 即被拒绝）----" | tee -a "$EVID/rejected.log"
}

echo "############ 用例① 正常流程 ############" | tee -a "$EVID/normal.log"
as_owner
echo "--- ①.1 注册 $DA（真实 ECDSA 公钥）---" | tee -a "$EVID/normal.log"
invoke "{\"function\":\"RegisterDevice\",\"Args\":[\"$DA\",\"\",\"Org1-Maker\",\"TempSensor-X1\",\"$DEV_PUBKEY\"]}" 2>&1 | tee -a "$EVID/normal.log"
echo "--- ①.2 激活 $DA ---" | tee -a "$EVID/normal.log"
invoke "{\"function\":\"ActivateDevice\",\"Args\":[\"$DA\"]}" 2>&1 | tee -a "$EVID/normal.log"
echo "--- ①.3 授权 consumer1 ---" | tee -a "$EVID/normal.log"
invoke "{\"function\":\"AuthorizeAccess\",\"Args\":[\"$DA\",\"$C1_ID\"]}" 2>&1 | tee -a "$EVID/normal.log"
as_consumer1
echo "--- ①.4 consumer1 上链（真实签名）---" | tee -a "$EVID/normal.log"
invoke "{\"function\":\"ReportData\",\"Args\":[\"$DA\",\"temperature\",\"25.5\",\"0\",\"$DEV_SIG\"]}" 2>&1 | tee -a "$EVID/normal.log"
echo "--- ①.5 consumer1 查询数据 ---" | tee -a "$EVID/normal.log"
query "{\"function\":\"GetDeviceData\",\"Args\":[\"$DA\"]}" 2>&1 | tee -a "$EVID/normal.log"
as_owner
echo "--- ①.6 owner 查询授权 ---" | tee -a "$EVID/normal.log"
query "{\"function\":\"GetAuthorization\",\"Args\":[\"$DA\",\"$C1_ID\"]}" 2>&1 | tee -a "$EVID/normal.log"

echo "############ 用例② 重复注册 ############" | tee -a "$EVID/rejected.log"
as_owner
rejected "重复注册 $DA" "{\"function\":\"RegisterDevice\",\"Args\":[\"$DA\",\"\",\"Org1-Maker\",\"TempSensor-X1\",\"$DEV_PUBKEY\"]}"

echo "############ 用例③ 未授权上链 ############" | tee -a "$EVID/rejected.log"
as_owner
echo "--- 状态证明：consumer2 对 $DA 无授权 ---" | tee -a "$EVID/rejected.log"
query "{\"function\":\"GetAuthorization\",\"Args\":[\"$DA\",\"$C2_ID\"]}" 2>&1 | tee -a "$EVID/rejected.log"
as_consumer2
rejected "未授权上链：consumer2 ReportData $DA" "{\"function\":\"ReportData\",\"Args\":[\"$DA\",\"temperature\",\"99\",\"0\",\"sig-unauth\"]}"

echo "############ 用例④ 越权上链 ############" | tee -a "$EVID/rejected.log"
as_owner
echo "--- 预置：注册并激活 $DB ---" | tee -a "$EVID/rejected.log"
invoke "{\"function\":\"RegisterDevice\",\"Args\":[\"$DB\",\"\",\"Org1-Maker\",\"HumidSensor-H1\",\"$DEV_PUBKEY\"]}" 2>&1 | tee -a "$EVID/rejected.log"
invoke "{\"function\":\"ActivateDevice\",\"Args\":[\"$DB\"]}" 2>&1 | tee -a "$EVID/rejected.log"
echo "--- 状态证明：consumer1 有 $DA 授权、无 $DB 授权 ---" | tee -a "$EVID/rejected.log"
query "{\"function\":\"GetAuthorization\",\"Args\":[\"$DA\",\"$C1_ID\"]}" 2>&1 | tee -a "$EVID/rejected.log"
query "{\"function\":\"GetAuthorization\",\"Args\":[\"$DB\",\"$C1_ID\"]}" 2>&1 | tee -a "$EVID/rejected.log"
as_consumer1
rejected "越权上链：consumer1 ReportData $DB" "{\"function\":\"ReportData\",\"Args\":[\"$DB\",\"humidity\",\"60\",\"0\",\"sig-overreach\"]}"

echo "############ 用例⑤ 过期(已撤销)授权上链 ############" | tee -a "$EVID/rejected.log"
as_owner
echo "--- 预置：撤销 consumer1 对 $DA 的授权 ---" | tee -a "$EVID/rejected.log"
invoke "{\"function\":\"RevokeAccess\",\"Args\":[\"$DA\",\"$C1_ID\"]}" 2>&1 | tee -a "$EVID/rejected.log"
echo "--- 状态证明：active=false ---" | tee -a "$EVID/rejected.log"
query "{\"function\":\"GetAuthorization\",\"Args\":[\"$DA\",\"$C1_ID\"]}" 2>&1 | tee -a "$EVID/rejected.log"
as_consumer1
rejected "过期授权上链：consumer1 ReportData $DA" "{\"function\":\"ReportData\",\"Args\":[\"$DA\",\"temperature\",\"26.0\",\"0\",\"sig-expired\"]}"

echo "############ 用例⑥ 未授权读取数据（回归）############" | tee -a "$EVID/rejected.log"
as_consumer2
echo "========== 未授权读取：consumer2 GetDeviceData $DA ==========" | tee -a "$EVID/rejected.log"
query "{\"function\":\"GetDeviceData\",\"Args\":[\"$DA\"]}" 2>&1 | tee -a "$EVID/rejected.log"
echo "---- 退出码 = ${PIPESTATUS[0]}（非 0 即被拒绝）----" | tee -a "$EVID/rejected.log"

echo "############ 用例⑦ 伪造签名上报（回归）############" | tee -a "$EVID/rejected.log"
as_owner
BADSIG=$(printf '00%.0s' $(seq 1 64))
rejected "伪造签名：owner ReportData $DA（错误签名）" "{\"function\":\"ReportData\",\"Args\":[\"$DA\",\"temperature\",\"30.0\",\"0\",\"$BADSIG\"]}"

echo ""
echo "===== 完成。证据：$EVID/normal.log 与 $EVID/rejected.log ====="
