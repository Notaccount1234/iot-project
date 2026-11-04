网安 202401050861

# IoT 可信应用链码（Hyperledger Fabric v2.2）

IoT 数据上链 / 存证溯源链码（链码名 `iotcc`）：覆盖「主体注册、授权管理、数据上链、查询追溯、操作审计」五类，共 **17 个对外接口**。

> 权限符号：🔒 仅 `Org1MSP`（平台管理组织）可调 · 🌐 联盟内任意组织可调

## 一、目录结构

```
iot_chaincode/
├── chaincode.go      # 主链码（17 个业务接口）
├── go.mod / go.sum   # Go 依赖（contractapi v1.2.2）
├── vendor/           # 依赖（支持离线打包部署）
├── deploy.sh         # 一键部署脚本（启动 test-network + 打包/安装/提交 iotcc）
├── fr_test.sh        # FR1–FR8 功能验收脚本（正常 + 攻击/异常用例）
└── caliper/          # Hyperledger Caliper 0.6.0 压测工程
```

## 二、数据结构

| 结构 | 字段 |
|---|---|
| `User` | `key`、`username`、`phone`、`passwordHash`、`role`、`createTime` |
| `Device` | `deviceID`、`deviceName`、`deviceType`、`owner`、`groupID`、`status`、`manufacturer`、`authorizedUntil` |
| `Group` | `groupID`、`groupName`、`ownerOrg`、`deviceList[]`、`description` |
| `DataHash` | `hashValue`、`deviceID`、`timestamp`、`txID` |
| `Operation` | `txID`、`opType`、`detail`、`callerMSP`、`time` |

状态 key 前缀：`USER_` / `DEVICE_` / `GROUP_` / `DATAHASH_` / `OPERATION_` / `OPSTAT_`。

## 三、接口清单（17 个）

### 注册类

**1. AddUser** 🔒
```go
AddUser(key, username, phone, passwordHash, role string) error
```
注册用户（`USER_<key>`，key 唯一，已存在则拒绝）。仅存 `passwordHash` 不存明文；成功后写审计并累加 `OPSTAT_AddUser`。

**2. AddDevice** 🔒
```go
AddDevice(deviceID, name, devType, owner, manufacturer, authorizedUntil string) error
```
注册设备（`DEVICE_<deviceID>`，唯一）。`owner` 用户必须已存在；`authorizedUntil` 为 RFC3339，过期后禁止上链。

### 授权管理类

**3. AddGroup** 🔒
```go
AddGroup(groupID, groupName, ownerOrg, desc string) error
```
创建分组（`GROUP_<groupID>`，唯一），`deviceList` 初始为空。

**4. AddDeviceToGroup** 🔒
```go
AddDeviceToGroup(groupID, deviceID string) error
```
设备入组：分组与设备均须已存在；双向更新 `Group.DeviceList` 与 `Device.GroupID`。

**5. ExpireAuthorization** 🔒
```go
ExpireAuthorization(deviceID, until string) error
```
管理员修改设备授权有效期（RFC3339）；改为过去时间即可演示「过期操作被拒」。

### 数据上链类

**6. UploadDataHash** 🔒
```go
UploadDataHash(deviceID, timestamp, hashValue string) error
```
上传链下原始数据的 SHA-256 摘要存证（`DATAHASH_<deviceID>_<timestamp>`）。校验：设备已注册 → 调用方 Org1MSP → 同键未写（不可覆盖）→ 授权未过期。

### 查询 / 追溯类

**7. QueryDevice** 🌐
```go
QueryDevice(deviceID string) (*Device, error)
```
按 deviceID 查设备台账。

**8. QueryAllDevices** 🔒
```go
QueryAllDevices() ([]*Device, error)
```
全量遍历设备，仅 Org1MSP 审计方。

**9. QueryDataHash** 🌐
```go
QueryDataHash(deviceID, timestamp string) (*DataHash, error)
```
按 `deviceID + timestamp` 追溯存证记录。

### 用户信息维护

**10. UpdateUser** 🔒
```go
UpdateUser(key, username, phone, role string) error
```
更新用户名/手机号/角色；`passwordHash` 与 `createTime` 不变。

**11. QueryUser** 🌐
```go
QueryUser(key string) (*User, error)
```
查询单个用户（返回完整 `User`，含 `passwordHash`/`phone`）。

**12. QueryAllUsers** 🔒
```go
QueryAllUsers() ([]*User, error)
```
全量遍历用户，仅 Org1MSP。

### 设备台账维护

**13. ModifyDevice** 🔒
```go
ModifyDevice(deviceID, name, manufacturer, authorizedUntil string) error
```
改设备名/厂商/授权有效期（传空串保持原值）。

### 分组查询

**14. QueryGroup** 🌐
```go
QueryGroup(groupID string) (*Group, error)
```

**15. QueryAllGroups** 🔒
```go
QueryAllGroups() ([]*Group, error)
```

### 操作审计（FR8）

**16. QueryOperations** 🔒
```go
QueryOperations(opType string) ([]*Operation, error)
```
`opType` 传 `ALL` 返回全部，否则按操作类型过滤。

**17. CountOperations** 🔒
```go
CountOperations() (map[string]int, error)
```
统计各操作类型调用次数（`OPSTAT_<opType>`）。

## 四、权限总览

| 权限 | 接口 |
|---|---|
| 🔒 仅 Org1MSP | 全部写操作 + `QueryAllDevices` / `QueryAllUsers` / `QueryAllGroups` / `QueryOperations` / `CountOperations` |
| 🌐 联盟内开放 | `QueryDevice` / `QueryDataHash` / `QueryUser` / `QueryGroup` |

## 五、部署与功能验收

```bash
bash deploy.sh    # 启动 test-network，打包/安装/提交 iotcc
bash fr_test.sh   # FR1–FR8 全覆盖验收（19 正常用例 + 12 攻击/异常用例）
```

## 六、Caliper 压测

```bash
cd caliper
npx caliper bind --caliper-bind-sut fabric:2.2   # 首次需绑定（或复用已有 node_modules）
npx caliper launch manager --caliper-workspace . \
  --caliper-benchconfig benchmarks/config.yaml \
  --caliper-networkconfig networks/network-config.yaml
```

场景：S1 数据上链 `UploadDataHash` / S2 设备注册 `AddDevice` / S3 查询 `QueryDevice` / S4 混合负载。

## 七、自审 Checklist

- [x] 所有写操作（Add*）均有重复 key 校验，不覆盖已有数据
- [x] `AddDevice` 校验 owner 用户存在，无孤儿设备
- [x] `AddDeviceToGroup` 双向更新 `Group.DeviceList` 与 `Device.GroupID`
- [x] `UploadDataHash` 校验设备已注册 + 授权未过期 + 同键不可覆盖
- [x] 密码只存 `passwordHash`，无明文落账本
- [x] 原始数据不上链，仅存 `hashValue`
- [x] 查询类方法在 key 不存在时返回明确 error
- [x] 全量遍历/审计接口限 Org1MSP
- [ ] 单条 `QueryUser` 开放且含 `passwordHash`（待修：脱敏或限权）
- [ ] `OPSTAT_` 共享计数器并发写会 MVCC 冲突（待修：改查询时聚合）
- [x] 状态 key 有前缀，避免命名空间冲突
- [x] `main()` 用 `contractapi.NewChaincode` + `Start()`
