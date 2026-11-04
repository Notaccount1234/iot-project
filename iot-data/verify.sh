#!/bin/bash
# iot-data 链码在 mychannel 上的交易验证脚本
# invoke 均加 --waitForEvent 等待交易提交，避免读写时序问题
# 前置：test-network 已启动、mychannel 已创建、iot-data 链码已部署

source "$(dirname "$0")/env.sh"

CH=iot-data
DEV=dev2
ORDERER="localhost:7050"
ORDERER_HOST="orderer.example.com"

# MAJORITY 背书策略：invoke 需 org1 + org2 两个 peer 共同背书
ENDORSE="--peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_TLS --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_TLS"
WAIT="--waitForEvent --waitForEventTimeout 30s"

invoke() {
  peer chaincode invoke -o "$ORDERER" --ordererTLSHostnameOverride "$ORDERER_HOST" --tls --cafile "$ORDERER_CA" \
    -C mychannel -n "$CH" $ENDORSE $WAIT -c "$1"
}
query() {
  peer chaincode query -C mychannel -n "$CH" -c "$1"
}

echo "========== 1. 注册设备 $DEV（owner 留空 -> 自动记为调用方身份）=========="
invoke "{\"function\":\"RegisterDevice\",\"Args\":[\"$DEV\",\"\",\"Org1-Maker\",\"TempSensor-X1\",\"pubkey-abc123\"]}"

echo ""
echo "========== 2. 激活设备 $DEV（0 -> 1）=========="
invoke "{\"function\":\"ActivateDevice\",\"Args\":[\"$DEV\"]}"

echo ""
echo "========== 3. 查询设备 $DEV =========="
query "{\"function\":\"GetDevice\",\"Args\":[\"$DEV\"]}"

echo ""
echo "========== 4. 授权 consumer1 读取 $DEV 数据 =========="
invoke "{\"function\":\"AuthorizeAccess\",\"Args\":[\"$DEV\",\"consumer1\"]}"

echo ""
echo "========== 5. $DEV 直连上报数据（timestamp=0 -> 取当前时间）=========="
invoke "{\"function\":\"ReportData\",\"Args\":[\"$DEV\",\"temperature\",\"25.5\",\"0\",\"sig-demo\"]}"

echo ""
echo "========== 6. 查询 $DEV 的全部数据记录 =========="
query "{\"function\":\"GetDeviceData\",\"Args\":[\"$DEV\"]}"

echo ""
echo "========== 7. 查询 $DEV 对 consumer1 的授权 =========="
query "{\"function\":\"GetAuthorization\",\"Args\":[\"$DEV\",\"consumer1\"]}"

echo ""
echo "========== 8. 查询全部设备 =========="
query "{\"function\":\"GetAllDevices\",\"Args\":[]}"

echo ""
echo "========== 9. 撤销 consumer1 授权 =========="
invoke "{\"function\":\"RevokeAccess\",\"Args\":[\"$DEV\",\"consumer1\"]}"

echo ""
echo "========== 10. 吊销设备 $DEV（-> 2）=========="
invoke "{\"function\":\"RevokeDevice\",\"Args\":[\"$DEV\"]}"

echo ""
echo "========== 11. 再次查询 $DEV（status 应为 2 已吊销）=========="
query "{\"function\":\"GetDevice\",\"Args\":[\"$DEV\"]}"
