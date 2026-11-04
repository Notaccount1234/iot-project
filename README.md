
网安 202401050861
# Fabric IoT 链码项目

基于 Hyperledger Fabric 的 IoT 数据上链 / 存证溯源链码，含部署脚本、功能验收脚本与 Caliper 性能压测工程。

## 目录

| 目录 | 说明 |
|---|---|
| `iot_chaincode/` | IoT 可信存证/溯源链码（链码名 `iotcc`）：设备/用户/分组注册、授权管理、数据哈希上链、查询追溯、操作审计。含 `deploy.sh` 一键部署、`fr_test.sh` 功能验收、`caliper/` 压测 |
| `iot-data/` | IoT 数据上链链码（含 ECDSA 验签、确定性时间戳、授权校验）。含 `acceptance-test.sh` / `verify.sh` 验收、`caliper/` 压测 |

## 环境

- Hyperledger Fabric v2.2（`test-network`，2 组织 × 1 peer + 1 orderer）
- Go 1.20+、Node.js 18+
- Hyperledger Caliper 0.6.0（`fabric:2.2` 绑定）

## 快速开始

### 部署链码（iot_chaincode）

```bash
cd iot_chaincode
bash deploy.sh          # 自动启动 test-network 并打包安装提交 iotcc
bash fr_test.sh         # FR1–FR8 功能验收（含攻击/异常用例）
```

### Caliper 压测（iot_chaincode）

```bash
cd iot_chaincode/caliper
# 首次需绑定 fabric SDK（或复用已有 node_modules）
npx caliper bind --caliper-bind-sut fabric:2.2
# 冒烟测试
npx caliper launch manager --caliper-workspace . \
  --caliper-benchconfig benchmarks/smoke.yaml \
  --caliper-networkconfig networks/network-config.yaml
# 正式压测（S1 上链 / S2 设备注册 / S3 查询 / S4 混合）
npx caliper launch manager --caliper-workspace . \
  --caliper-benchconfig benchmarks/config.yaml \
  --caliper-networkconfig networks/network-config.yaml
```

## 说明

- `vendor/` 已提交，用于链码离线打包部署（`deploy.sh` 依赖）；如需精简可删除后用 `go mod vendor` 重新生成。
- `node_modules/` 不提交，用 `npm install` / `caliper bind` 重新生成。
- 链码安全审查见 `iot_chaincode/` 与项目根目录相关文档。
