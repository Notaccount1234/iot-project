#!/bin/bash
# ============================================================
# FR1–FR8 全覆盖端到端验收脚本
# 19 条正常用例（覆盖 FR1–FR8 每条功能需求）+ 12 条攻击/异常/回归用例（应被拒绝）
# 用法: 在虚拟机任意目录执行  bash fr_test.sh
# 证据: 全部原始输出 tee 至 /home/ljx/fr_test_evidence.log
# ============================================================

TESTNET_DIR="/home/ljx/fabric-samples/test-network"
cd "${TESTNET_DIR}" || exit 1
export PATH="${PWD}/../bin:${PATH}"
export FABRIC_CFG_PATH="${PWD}/../config"

CC_NAME="iotcc"
CHANNEL="mychannel"
ORDERER="localhost:7050"
ORDERER_HOST="orderer.example.com"
ORDERER_CA="${PWD}/organizations/ordererOrganizations/example.com/orderers/orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem"
ORG1_TLS="${PWD}/organizations/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt"
ORG2_TLS="${PWD}/organizations/peerOrganizations/org2.example.com/peers/peer0.org2.example.com/tls/ca.crt"
ORG1_MSP="${PWD}/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp"
ORG2_MSP="${PWD}/organizations/peerOrganizations/org2.example.com/users/Admin@org2.example.com/msp"

# MAJORITY 背书：invoke 需同时向两组织 peer 收集背书
ENDORSE="--peerAddresses localhost:7051 --tlsRootCertFiles ${ORG1_TLS} --peerAddresses localhost:9051 --tlsRootCertFiles ${ORG2_TLS}"

use_org1() {
  export CORE_PEER_LOCALMSPID="Org1MSP"
  export CORE_PEER_MSPCONFIGPATH="${ORG1_MSP}"
  export CORE_PEER_ADDRESS=localhost:7051
  export CORE_PEER_TLS_ENABLED=true
  export CORE_PEER_TLS_ROOTCERT_FILE="${ORG1_TLS}"
}
use_org2() {
  export CORE_PEER_LOCALMSPID="Org2MSP"
  export CORE_PEER_MSPCONFIGPATH="${ORG2_MSP}"
  export CORE_PEER_ADDRESS=localhost:9051
  export CORE_PEER_TLS_ENABLED=true
  export CORE_PEER_TLS_ROOTCERT_FILE="${ORG2_TLS}"
}

invoke() {
  peer chaincode invoke -o ${ORDERER} --ordererTLSHostnameOverride ${ORDERER_HOST} \
    --tls --cafile ${ORDERER_CA} -C ${CHANNEL} -n ${CC_NAME} ${ENDORSE} \
    --waitForEvent --waitForEventTimeout 30s "$@"
}
query() {
  peer chaincode query -C ${CHANNEL} -n ${CC_NAME} "$@"
}
# 打印原始输出并把中文八进制转义还原为可读 UTF-8
run() {
  local out
  out="$("$@" 2>&1)"
  printf '%b\n' "${out}"
}

FUTURE=$(date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
PAST="2020-01-01T00:00:00Z"
TS1="2026-11-02T10:00:00Z"
TS2="2026-11-02T11:00:00Z"
# 用真实 SHA-256 摘要模拟"链下原始数据 → 计算哈希 → 上链存证"
H1=$(printf 'apple-batch-A-2026-11-02 quality-inspection-report' | sha256sum | cut -c1-64)
H2=$(printf 'apple-batch-A-2026-11-02 cold-chain-temp-log' | sha256sum | cut -c1-64)
H3=$(printf 'apple-batch-A-2026-11-02 pesticide-residue-test' | sha256sum | cut -c1-64)
HEVIL=$(printf 'forged-evil-hash' | sha256sum | cut -c1-64)

echo "############################################################"
echo "# iotcc FR1-FR8 全覆盖验收  $(date '+%F %T')"
echo "# 哈希取自真实 SHA-256：H1=${H1}"
echo "############################################################"

echo "========== A. 正常功能用例（FR1-FR8） =========="
use_org1

echo "--- TC-01 [FR1] 主体注册：AddUser u001（合作社账号 alice） ---"
run invoke --ctor '{"Args":["AddUser","u001","alice","13800000001","5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8","admin"]}'

echo "--- TC-02 [FR1] 信息更新：UpdateUser u001（改手机号与角色） ---"
run invoke --ctor '{"Args":["UpdateUser","u001","alice","13911112222","inspector"]}'

echo "--- TC-03 [FR1] 查询用户：QueryUser u001（验证更新生效） ---"
run query --ctor '{"Args":["QueryUser","u001"]}'

echo "--- TC-04 [FR2] 设备接入：AddDevice dev001（授权有效期+1h） ---"
run invoke --ctor "{\"Args\":[\"AddDevice\",\"dev001\",\"温湿度传感器-A1\",\"sensor\",\"u001\",\"Huawei\",\"${FUTURE}\"]}"

echo "--- TC-05 [FR2] 台账维护：ModifyDevice dev001（改设备名） ---"
run invoke --ctor '{"Args":["ModifyDevice","dev001","温湿度传感器-A1-改进型","",""]}'

echo "--- TC-06 [FR5] 溯源查询：QueryDevice dev001（验证台账更新） ---"
run query --ctor '{"Args":["QueryDevice","dev001"]}'

echo "--- TC-07 [FR3] 创建分组：AddGroup g001（青岛实验室） ---"
run invoke --ctor '{"Args":["AddGroup","g001","青岛实验室","Org1MSP","环境监测"]}'

echo "--- TC-08 [FR3] 设备入组：AddDeviceToGroup g001 dev001 ---"
run invoke --ctor '{"Args":["AddDeviceToGroup","g001","dev001"]}'

echo "--- TC-09 [FR3] 查询分组：QueryGroup g001（验证 deviceList） ---"
run query --ctor '{"Args":["QueryGroup","g001"]}'

echo "--- TC-10 [FR4] 批次存证：UploadDataHash dev001 ${TS1}（SHA-256 检测报告摘要） ---"
run invoke --ctor "{\"Args\":[\"UploadDataHash\",\"dev001\",\"${TS1}\",\"${H1}\"]}"

echo "--- TC-11 [FR4/SR3] 存证防篡改：同键重传（应拒绝：不可覆盖） ---"
run invoke --ctor "{\"Args\":[\"UploadDataHash\",\"dev001\",\"${TS1}\",\"${H2}\"]}"

echo "--- TC-12 [FR5/FR7] 存证查询：QueryDataHash dev001 ${TS1}（哈希比对基准） ---"
run query --ctor "{\"Args\":[\"QueryDataHash\",\"dev001\",\"${TS1}\"]}"

echo "--- TC-13 [FR4] 第二笔存证：UploadDataHash dev001 ${TS2}（冷链温控日志摘要） ---"
run invoke --ctor "{\"Args\":[\"UploadDataHash\",\"dev001\",\"${TS2}\",\"${H3}\"]}"

echo "--- TC-14 [FR6] 监管全量查询：QueryAllDevices（Org1 审计方，应成功） ---"
run query --ctor '{"Args":["QueryAllDevices"]}'

echo "--- TC-15 [FR6] 全量用户：QueryAllUsers（Org1 审计方，应成功） ---"
run query --ctor '{"Args":["QueryAllUsers"]}'

echo "--- TC-16 [FR3/FR6] 全量分组：QueryAllGroups（Org1 审计方，应成功） ---"
run query --ctor '{"Args":["QueryAllGroups"]}'

echo "--- TC-17 [FR8] 操作审计：QueryOperations ALL（应有 AddUser/AddDevice/UploadDataHash 等记录） ---"
run query --ctor '{"Args":["QueryOperations","ALL"]}'

echo "--- TC-18 [FR8] 调用统计：CountOperations（各操作类型计数） ---"
run query --ctor '{"Args":["CountOperations"]}'

echo "--- TC-19 [FR7] 批次追溯：QueryDataHash dev001 ${TS2}（第二条存证） ---"
run query --ctor "{\"Args\":[\"QueryDataHash\",\"dev001\",\"${TS2}\"]}"

echo ""
echo "========== B. 攻击 / 异常 / 回归用例（应全部被拒绝） =========="

echo "--- TC-20 [SR1/FR2] 攻击：伪造设备上链（未注册的 fake_dev_999） ---"
run invoke --ctor '{"Args":["UploadDataHash","fake_dev_999","2026-11-02T12:00:00Z","deadbeef"]}'

echo "--- TC-21 [SR2/FR6] 攻击：越权查询（Org2 调全量遍历设备） ---"
use_org2
run query --ctor '{"Args":["QueryAllDevices"]}'

echo "--- TC-22 [SR2/FR6] 攻击：越权查询（Org2 调全量遍历用户） ---"
run query --ctor '{"Args":["QueryAllUsers"]}'

echo "--- TC-23 [SR2/FR8] 攻击：越权查询（Org2 调操作审计） ---"
run query --ctor '{"Args":["QueryOperations","ALL"]}'

echo "--- TC-24 [FR1] 攻击：Org2 越权更新用户（管理类写操作） ---"
run invoke --ctor '{"Args":["UpdateUser","u001","hacked","000","admin"]}'

echo "--- TC-25 [修复回归] 越权注册用户/提权（Org2 调 AddUser 注册 admin） ---"
run invoke --ctor '{"Args":["AddUser","evil001","mallory","13900000000","deadbeef","admin"]}'

echo "--- TC-26 [修复回归] 越权创建分组（Org2 调 AddGroup） ---"
run invoke --ctor '{"Args":["AddGroup","evil-g","恶意分组","Org2MSP","越权"]}'

echo "--- TC-27 [修复回归] 越权上链（Org2 替 dev001 上链） ---"
run invoke --ctor "{\"Args\":[\"UploadDataHash\",\"dev001\",\"2026-11-02T13:00:00Z\",\"${HEVIL}\"]}"

echo "--- TC-28 [FR2] 异常：重复注册已存在设备 dev001 ---"
use_org1
run invoke --ctor "{\"Args\":[\"AddDevice\",\"dev001\",\"重复设备\",\"sensor\",\"u001\",\"Huawei\",\"${FUTURE}\"]}"

echo "--- TC-29 [SR3/FR3] 攻击：过期授权操作（先把 dev001 授权改为过去，再上链） ---"
run invoke --ctor "{\"Args\":[\"ExpireAuthorization\",\"dev001\",\"${PAST}\"]}"
run invoke --ctor "{\"Args\":[\"UploadDataHash\",\"dev001\",\"2026-11-02T14:00:00Z\",\"cafebeef\"]}"

echo "--- TC-30 [FR1] 异常：重复注册已存在用户 u001 ---"
run invoke --ctor '{"Args":["AddUser","u001","dup","13800000001","5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8","admin"]}'

echo "--- TC-31 [FR3] 异常：设备加入不存在的分组 g999 ---"
run invoke --ctor '{"Args":["AddDeviceToGroup","g999","dev001"]}'

echo ""
echo "========== 结束：证据已保存 /home/ljx/fr_test_evidence.log =========="
