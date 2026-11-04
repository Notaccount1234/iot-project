'use strict';

const { WorkloadModuleBase } = require('@hyperledger/caliper-core');
const crypto = require('crypto');

/**
 * iot_chaincode (iotcc) 性能压测工作量模块。
 *
 * 由 benchmark config 的 workload.arguments.function 指定本 round 执行的合约函数：
 *   - AddUser         : 每笔交易注册一个新用户（写，测注册吞吐；无预置）
 *   - AddDevice       : 每笔交易注册一台新设备（写，测设备注册吞吐；预置 1 个归属用户）
 *   - AddGroup        : 每笔交易创建一个新分组（写；无预置）
 *   - UploadDataHash  : 对预置设备上传数据哈希存证（写，核心 IoT 上链路径）
 *   - QueryDevice     : 查询预置设备台账（读）
 *   - QueryDataHash   : 追溯预置设备的存证记录（读）
 *   - Mixed           : 按 readRatio 概率在 UploadDataHash / QueryDevice 间切换（读写混合）
 *
 * 说明：
 *   - 链码所有写操作经 requireAdmin() 仅允许 Org1MSP，故身份用 Org1 Admin（见 network-config.yaml）。
 *   - UploadDataHash 要求设备已注册且未过期、且 (deviceID, timestamp) 不可重复，
 *     故每笔交易用唯一 timestamp；预置设备授权期设为 2099 年（不触发过期拦截）。
 *   - 每次运行给 key 追加唯一后缀，避免重跑时 AddUser/AddDevice 撞上已存在的 key。
 */
class IoTCCWorkload extends WorkloadModuleBase {
    constructor() {
        super();
        this.submitCount = 0;   // 本 round 已提交交易计数
        this.devices = [];      // 预置设备 deviceID 列表
    }

    async initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext) {
        await super.initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext);

        const a = roundArguments;
        this.contractId = a.contractId || 'iotcc';
        this.func = a.function || 'UploadDataHash';
        this.deviceCount = a.deviceCount || 50;
        // 每次运行追加唯一后缀，避免重跑时 AddUser/AddDevice 撞上已存在的 key
        this.uniqueSuffix = `${a.devicePrefix || 'bench'}-${Date.now()}`;
        this.ownerKey = `owner-${this.uniqueSuffix}`;
        this.authorizedUntil = a.authorizedUntil || '2099-12-31T23:59:59Z';
        this.readRatio = (typeof a.readRatio === 'number') ? a.readRatio : 0;

        switch (this.func) {
            case 'AddUser':
            case 'AddGroup':
                // 无需预置
                break;
            case 'AddDevice':
                // 仅需预置一个归属用户（AddDevice 校验 owner 用户存在）
                await this._setupOwner();
                break;
            default:
                // UploadDataHash / QueryDevice / QueryDataHash / Mixed：预置用户 + 设备
                await this._setupDevices();
                break;
        }
    }

    _hash(text) {
        return crypto.createHash('sha256').update(text).digest('hex');
    }

    /**
     * 提交一批 setup 交易并检查是否全部成功，任一失败即抛错（快速暴露 setup 问题）。
     *
     * 注意：这里必须「逐笔串行」提交，不能并发。原因——iotcc 链码的 recordOperation()
     * 会对共享计数器 OPSTAT_<opType> 做「读-改-写」，并发写同类型操作时多个交易读同一版本、
     * 再各自 +1 写回，落在同一区块即触发 MVCC_READ_CONFLICT。串行（每笔单独区块）可规避。
     */
    async _sendAll(requests, phase) {
        for (let i = 0; i < requests.length; i++) {
            const result = await this.sutAdapter.sendRequests(requests[i]);
            if (!result.IsCommitted()) {
                throw new Error(`setup phase "${phase}": tx ${i}/${requests.length} not committed`);
            }
        }
    }

    /** 预置一个归属用户（AddDevice 的 owner） */
    async _setupOwner() {
        await this._sendAll([{
            contractId: this.contractId,
            contractFunction: 'AddUser',
            contractArguments: [this.ownerKey, 'bench-owner', '13800000000', this._hash('bench-pwd'), 'user'],
            readOnly: false,
        }], 'add-user');
    }

    /** 预置：注册归属用户 + N 台设备；查询存证场景再预置一条存证 */
    async _setupDevices() {
        await this._setupOwner();
        this.devices = [];
        const requests = [];
        for (let i = 0; i < this.deviceCount; i++) {
            const deviceID = `${this.uniqueSuffix}-${i}`;
            this.devices.push(deviceID);
            requests.push({
                contractId: this.contractId,
                contractFunction: 'AddDevice',
                contractArguments: [deviceID, 'bench-sensor', 'sensor', this.ownerKey, 'bench-maker', this.authorizedUntil],
                readOnly: false,
            });
        }
        await this._sendAll(requests, 'add-devices');

        if (this.func === 'QueryDataHash') {
            // 每条设备预置一条 (deviceID, seedTimestamp) 存证，供 QueryDataHash 命中
            this.seedTimestamp = `seed-${Date.now()}`;
            const uploads = this.devices.map(d => ({
                contractId: this.contractId,
                contractFunction: 'UploadDataHash',
                contractArguments: [d, this.seedTimestamp, this._hash(`seed-${d}`)],
                readOnly: false,
            }));
            await this._sendAll(uploads, 'seed-datahash');
        }
    }

    /** 每笔被测交易，返回 Caliper 请求对象 */
    async submitTransaction() {
        this.submitCount++;
        let request;
        switch (this.func) {
            case 'AddUser': {
                request = {
                    contractId: this.contractId,
                    contractFunction: 'AddUser',
                    contractArguments: [`${this.uniqueSuffix}-user-${this.submitCount}`, 'bench', '13800000000', this._hash(`pwd-${this.submitCount}`), 'user'],
                    readOnly: false,
                };
                break;
            }
            case 'AddDevice': {
                request = {
                    contractId: this.contractId,
                    contractFunction: 'AddDevice',
                    contractArguments: [`${this.uniqueSuffix}-dev-${this.submitCount}`, 'bench-sensor', 'sensor', this.ownerKey, 'bench-maker', this.authorizedUntil],
                    readOnly: false,
                };
                break;
            }
            case 'AddGroup': {
                request = {
                    contractId: this.contractId,
                    contractFunction: 'AddGroup',
                    contractArguments: [`${this.uniqueSuffix}-group-${this.submitCount}`, 'bench-group', 'Org1MSP', 'bench'],
                    readOnly: false,
                };
                break;
            }
            case 'QueryDevice': {
                request = this._readDevice();
                break;
            }
            case 'QueryDataHash': {
                request = this._readDataHash();
                break;
            }
            case 'Mixed': {
                request = (Math.random() < this.readRatio) ? this._readDevice() : this._uploadTx();
                break;
            }
            case 'UploadDataHash':
            default: {
                request = this._uploadTx();
                break;
            }
        }
        // Caliper 0.6.0 契约：submitTransaction 必须把请求交给 SUT 适配器提交并返回其结果，
        // 仅 return 请求对象会导致 0 笔交易被提交。
        return this.sutAdapter.sendRequests(request);
    }

    _uploadTx() {
        const deviceID = this.devices[this.submitCount % this.devices.length];
        const timestamp = `${Date.now()}-${this.submitCount}`; // 唯一，避免 DATAHASH_<device>_<ts> 键冲突
        return {
            contractId: this.contractId,
            contractFunction: 'UploadDataHash',
            contractArguments: [deviceID, timestamp, this._hash(`payload-${deviceID}-${timestamp}`)],
            readOnly: false,
        };
    }

    _readDevice() {
        const deviceID = this.devices[this.submitCount % this.devices.length];
        return {
            contractId: this.contractId,
            contractFunction: 'QueryDevice',
            contractArguments: [deviceID],
            readOnly: true,
        };
    }

    _readDataHash() {
        const deviceID = this.devices[this.submitCount % this.devices.length];
        return {
            contractId: this.contractId,
            contractFunction: 'QueryDataHash',
            contractArguments: [deviceID, this.seedTimestamp],
            readOnly: true,
        };
    }

    async cleanupWorkloadModule() {
        // 无持久资源需释放
    }
}

function createWorkloadModule() {
    return new IoTCCWorkload();
}

module.exports.createWorkloadModule = createWorkloadModule;
