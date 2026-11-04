#!/bin/bash
# ============================================================
# Fabric test-network 一键部署 iot_chaincode 脚本（Fabric 2.2.0）
# 用法: bash deploy.sh（脚本自动切入 test-network 目录）
# 说明: 链码源码（含 go.mod + vendor/）位于 fabric-samples/chaincode/iot_chaincode
# ============================================================

set -euo pipefail

TESTNET_DIR="/home/ljx/fabric-samples/test-network"
if [ -d "${TESTNET_DIR}" ]; then
  cd "${TESTNET_DIR}"
else
  echo "找不到 test-network 目录: ${TESTNET_DIR}" >&2
  exit 1
fi

# 在宿主机直接调用 peer 二进制所需的环境
export PATH="${PWD}/../bin:/usr/lib/go-1.20/bin:${PATH}"
export FABRIC_CFG_PATH="${PWD}/../config"

CC_NAME="iotcc"
CC_VERSION="1.0"
CC_SEQUENCE="1"
CHANNEL_NAME="mychannel"
CC_SRC="/home/ljx/fabric-samples/chaincode/iot_chaincode"   # 链码源码目录（含 vendor）
CC_PKG="${CC_NAME}.tar.gz"

ORDERER_CA="${PWD}/organizations/ordererOrganizations/example.com/orderers/orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem"
ORG1_TLS="${PWD}/organizations/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt"
ORG2_TLS="${PWD}/organizations/peerOrganizations/org2.example.com/peers/peer0.org2.example.com/tls/ca.crt"
ORG1_MSP="${PWD}/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp"
ORG2_MSP="${PWD}/organizations/peerOrganizations/org2.example.com/users/Admin@org2.example.com/msp"

echo "==== [1/6] 清理旧网络并启动 test-network（2 Org + 1 Orderer，cryptogen 模式） ===="
./network.sh down 2>/dev/null || true
./network.sh up createChannel -c ${CHANNEL_NAME}

echo "==== [2/6] 拷贝链码源码（含 vendor 依赖）到 chaincode/ 目录 ===="
SRC_REAL=$(realpath ${CC_SRC})
DST_REAL=$(realpath ../chaincode/iot_chaincode 2>/dev/null || echo none)
if [ "${SRC_REAL}" != "${DST_REAL}" ]; then
  rm -rf ../chaincode/iot_chaincode
  mkdir -p ../chaincode/iot_chaincode
  cp -r ${CC_SRC}/. ../chaincode/iot_chaincode/
else
  echo "源码目录与打包目录一致，跳过拷贝"
fi

echo "==== [3/6] 打包链码 ===="
peer lifecycle chaincode package ${CC_PKG} \
  --path ../chaincode/iot_chaincode --lang golang --label ${CC_NAME}_${CC_VERSION}

# ---- 身份切换（TLS 开启，peer 与 orderer 均走 TLS）----
export CORE_PEER_TLS_ENABLED=true
as_org1() {
  export CORE_PEER_LOCALMSPID="Org1MSP"
  export CORE_PEER_MSPCONFIGPATH="${ORG1_MSP}"
  export CORE_PEER_ADDRESS=localhost:7051
  export CORE_PEER_TLS_ROOTCERT_FILE="${ORG1_TLS}"
}
as_org2() {
  export CORE_PEER_LOCALMSPID="Org2MSP"
  export CORE_PEER_MSPCONFIGPATH="${ORG2_MSP}"
  export CORE_PEER_ADDRESS=localhost:9051
  export CORE_PEER_TLS_ROOTCERT_FILE="${ORG2_TLS}"
}

echo "==== [4/6] Org1 / Org2 安装链码 ===="
as_org1
INSTALL_OUT="$(peer lifecycle chaincode install ${CC_PKG} 2>&1)"
echo "${INSTALL_OUT}"
# Fabric 2.2 无 calculatepackageid 子命令，从 install 输出提取 package-id
PKG_ID="$(echo "${INSTALL_OUT}" | sed -n 's/.*package identifier: \([^ ]*\).*/\1/p')"
echo "packageID = ${PKG_ID}"
[ -n "${PKG_ID}" ] || { echo "无法从 install 输出解析 package-id，中止"; exit 1; }

as_org2
peer lifecycle chaincode install ${CC_PKG}

echo "==== [5/6] 两组织批准链码定义（MAJORITY 背书 = 两组织都须批准） ===="
as_org1
peer lifecycle chaincode approveformyorg \
  -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com \
  --channelID ${CHANNEL_NAME} --name ${CC_NAME} --version ${CC_VERSION} \
  --package-id ${PKG_ID} --sequence ${CC_SEQUENCE} \
  --tls --cafile ${ORDERER_CA}

as_org2
peer lifecycle chaincode approveformyorg \
  -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com \
  --channelID ${CHANNEL_NAME} --name ${CC_NAME} --version ${CC_VERSION} \
  --package-id ${PKG_ID} --sequence ${CC_SEQUENCE} \
  --tls --cafile ${ORDERER_CA}

echo "==== [6/6] 提交链码定义（两组织背书）并查询提交状态 ===="
as_org1
peer lifecycle chaincode commit \
  -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com \
  --channelID ${CHANNEL_NAME} --name ${CC_NAME} --version ${CC_VERSION} \
  --sequence ${CC_SEQUENCE} \
  --tls --cafile ${ORDERER_CA} \
  --peerAddresses localhost:7051 --tlsRootCertFiles ${ORG1_TLS} \
  --peerAddresses localhost:9051 --tlsRootCertFiles ${ORG2_TLS}

peer lifecycle chaincode querycommitted \
  -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com \
  --channelID ${CHANNEL_NAME} --name ${CC_NAME} \
  --tls --cafile ${ORDERER_CA}

echo ""
echo "==== 部署完成：${CC_NAME} v${CC_VERSION} (seq ${CC_SEQUENCE}) 已提交到 ${CHANNEL_NAME} ===="
