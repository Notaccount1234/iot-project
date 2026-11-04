# IoT 可信应用链码（Hyperledger Fabric v2.20）

## 一、目录结构

```
iot_chaincode/
├── go.mod
├── chaincode.go      # 主链码，8 个功能方法
└── README.md
```

## 二、方法清单与场景覆盖

| 场景分类 | 方法名 | 说明 |
|---|---|---|
| 注册 | `AddUser` | 注册用户，幂等校验 |
| 注册 | `AddDevice` | 注册设备，校验 owner 用户存在 |
| 授权管理 | `AddGroup` | 创建数据源分组 |
| 授权管理 | `AddDeviceToGroup` | 把设备授权加入分组（双向索引一致） |
| 数据上链 | `UploadDataHash` | 上传数据 SHA-256 摘要（原始数据链下） |
| 查询/追溯 | `QueryDevice` | 按 deviceID 查设备台账 |
| 查询/追溯 | `QueryAllDevices` | 全量遍历设备（审计用） |
| 查询/追溯 | `QueryDataHash` | 按 deviceID+timestamp 追溯存证记录 |

共 **1 个链码 / 8 个方法**，覆盖题目要求的"注册、授权管理、数据上链、查询/追溯"四类。

## 三、自审 Checklist（合入前逐项打勾）

- [ ] 所有写操作（Add*）都有重复 key 校验，不会覆盖已有数据
- [ ] `AddDevice` 校验了 owner 用户存在，无孤儿设备
- [ ] `AddDeviceToGroup` 同时更新 Group.DeviceList 和 Device.GroupID，两端一致
- [ ] `UploadDataHash` 校验设备已注册，未注册设备不能上链
- [ ] 密码只存 `passwordHash`，无明文密码落账本
- [ ] 原始传感器数据不上链，仅存 `hashValue`
- [ ] 查询类方法在 key 不存在时返回明确 error，不返回 nil 空指针
- [ ] JSON 序列化/反序列化错误均被忽略但不影响主流程（作业规模可接受，生产应处理）
- [ ] 所有状态 key 有前缀（`USER_` / `DEVICE_` / `GROUP_` / `DATAHASH_`），避免命名空间冲突
- [ ] `main()` 用 `contractapi.NewChaincode` + `Start()`，符合 Fabric v2 链码规范

## 四、测试命令（按顺序 invoke，在 Fabric 测试网络 cli 容器内执行）

```bash
# 0) 环境变量（按你自己的网络改）
export CHANNEL_NAME=mychannel
export CC_NAME=iotcc
export CC_VERSION=1.0

# 1) 注册用户
peer chaincode invoke -o orderer.example.com:7050 \
  -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["AddUser","u001","alice","13800000001","<sha256pwd>","admin"]}'

# 2) 注册设备
peer chaincode invoke -o orderer.example.com:7050 \
  -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["AddDevice","dev001","温湿度传感器-A1","sensor","u001","Huawei"]}'

# 3) 创建分组
peer chaincode invoke -o orderer.example.com:7050 \
  -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["AddGroup","g001","青岛校区实验室","Org1MSP","环境监测组"]}'

# 4) 授权设备加入分组
peer chaincode invoke -o orderer.example.com:7050 \
  -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["AddDeviceToGroup","g001","dev001"]}'

# 5) 数据上链（上传哈希摘要）
peer chaincode invoke -o orderer.example.com:7050 \
  -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["UploadDataHash","dev001","2026-10-21T10:00:00Z","a1b2c3d4e5f6..."]}'

# 6) 查询设备
peer chaincode query -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["QueryDevice","dev001"]}'

# 7) 全量设备
peer chaincode query -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["QueryAllDevices"]}'

# 8) 追溯数据存证
peer chaincode query -C $CHANNEL_NAME -n $CC_NAME \
  --ctor '{"Args":["QueryDataHash","dev001","2026-10-21T10:00:00Z"]}'
```

## 五、合入标准

1. 上述 8 步依次执行无报错；
2. 重复执行第 1 步（AddUser 同一 key）应返回 `用户 u001 已存在`；
3. 用一个未注册的 deviceID 调 `UploadDataHash`，应返回 `未注册，禁止数据上链`；
4. `QueryDataHash` 返回的 `hashValue` 与本地对链下原始数据计算的 SHA-256 一致，即校验通过。

满足以上 4 条即视为本批次链码测试通过，可合入。
