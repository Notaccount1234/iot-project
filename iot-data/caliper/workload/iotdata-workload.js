'use strict';

const { WorkloadModuleBase } = require('@hyperledger/caliper-core');
const crypto = require('crypto');

/**
 * iot-data 链码性能测试工作量模块。
 *
 * 由 benchmark config 的 workload.arguments.function 指定本 round 执行的合约函数：
 *   - RegisterDevice : 每笔交易注册一台全新设备（写，测注册吞吐；无预置）
 *   - ReportData     : 对预置设备上报数据（写，含 ECDSA 验签，测上链吞吐）
 *   - GetDeviceData  : 查询预置设备的数据（读，测查询吞吐）
 *   - Mixed          : 按 readRatio 概率在 ReportData / GetDeviceData 间切换（读写混合）
 *
 * 签名规则与链码 verifyReportSignature 严格一致（已用 Go 侧交叉验证通过）：
 *   待签消息 = deviceID|dataType|value|rawTimestamp
 *   签名     = ECDSA(P-256) 对 sha256(消息) 的签名，输出 hex(r||s) 共 64 字节
 *   公钥     = hex 编码的 PKIX/DER(SPKI) P-256 公钥
 */
class IoTDataWorkload extends WorkloadModuleBase {
    constructor() {
        super();
        this.devices = [];      // 预置设备：[{id, pubHex, privKey}]
        this.submitCount = 0;   // 本 round 已提交交易计数
    }

    /**
     * 初始化：解析参数、预生成设备密钥，并对需要预置设备的函数做「注册→激活→上报一条」的 setup。
     */
    async initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext) {
        await super.initializeWorkloadModule(workerIndex, totalWorkers, roundIndex, roundArguments, sutAdapter, sutContext);

        const a = roundArguments;
        this.contractId = a.contractId || 'iot-data';
        this.func = a.function || 'ReportData';
        this.deviceCount = a.deviceCount || 50;
        // 每次运行追加唯一后缀，避免重跑时 RegisterDevice 撞上已存在的 deviceID
        // （RegisterDevice 不可重入，同名设备会返回 "already exists"）
        this.devicePrefix = `${a.devicePrefix || 'bench'}-${Date.now()}`;
        this.dataType = a.dataType || 'temperature';
        this.value = String(a.value);                    // 强制字符串，避免 YAML 数字类型混入合约参数
        this.readRatio = (typeof a.readRatio === 'number') ? a.readRatio : 0;
        this.baseTimestamp = Math.floor(Date.now() / 1000); // 上报时间戳基准（显式签名显式时间戳，不用 0 替换）

        if (this.func !== 'RegisterDevice') {
            for (let i = 0; i < this.deviceCount; i++) {
                this.devices.push(this._newDevice(`${this.devicePrefix}-${i}`));
            }
            await this._setupDevices();
        }
    }

    /** 生成一台设备的 ECDSA(P-256) 密钥对 */
    _newDevice(deviceID) {
        const { publicKey, privateKey } = crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' });
        return {
            id: deviceID,
            pubHex: publicKey.export({ type: 'spki', format: 'der' }).toString('hex'),
            privKey: privateKey,
        };
    }

    /** 对 deviceID|dataType|value|timestamp 做 ECDSA 签名，返回 hex(r||s) 64 字节 */
    _sign(device, dataType, value, timestamp) {
        const msg = [device.id, dataType, value, String(timestamp)].join('|');
        const sig = crypto.sign('sha256', Buffer.from(msg, 'utf8'), {
            key: device.privKey,
            dsaEncoding: 'ieee-p1363',
        });
        return sig.toString('hex');
    }

    /** 预置：三阶段批量并发（不同设备互相独立；同阶段内并发、阶段间串行） */
    async _setupDevices() {
        const register = this.devices.map(d => ({
            contractId: this.contractId,
            contractFunction: 'RegisterDevice',
            contractArguments: [d.id, '', 'bench-maker', 'bench-model', d.pubHex],
            readOnly: false,
        }));
        const activate = this.devices.map(d => ({
            contractId: this.contractId,
            contractFunction: 'ActivateDevice',
            contractArguments: [d.id],
            readOnly: false,
        }));
        const report = this.devices.map(d => ({
            contractId: this.contractId,
            contractFunction: 'ReportData',
            contractArguments: [d.id, this.dataType, this.value, '0', this._sign(d, this.dataType, this.value, 0)],
            readOnly: false,
        }));

        await this._sendAll(register, 'register');
        await this._sendAll(activate, 'activate');
        await this._sendAll(report, 'report');
    }

    /** 提交一批 setup 交易并检查是否全部成功，任一失败即抛错（快速暴露 setup 问题） */
    async _sendAll(requests, phase) {
        const results = await this.sutAdapter.sendRequests(requests);
        const failed = results.filter(r => !r.IsCommitted());
        if (failed.length > 0) {
            throw new Error(`setup phase "${phase}": ${failed.length}/${requests.length} transactions not committed`);
        }
    }

    /** 每笔被测交易，返回 Caliper 请求对象 */
    async submitTransaction() {
        this.submitCount++;
        let request;
        switch (this.func) {
            case 'RegisterDevice': {
                const d = this._newDevice(`${this.devicePrefix}-new-${this.workerIndex}-${this.submitCount}`);
                request = {
                    contractId: this.contractId,
                    contractFunction: 'RegisterDevice',
                    contractArguments: [d.id, '', 'bench-maker', 'bench-model', d.pubHex],
                    readOnly: false,
                };
                break;
            }
            case 'GetDeviceData': {
                request = this._readTx();
                break;
            }
            case 'Mixed': {
                request = (Math.random() < this.readRatio) ? this._readTx() : this._reportTx();
                break;
            }
            case 'ReportData':
            default: {
                request = this._reportTx();
                break;
            }
        }
        // Caliper 0.6.0 契约：submitTransaction 必须把请求交给 SUT 适配器提交并返回其结果，
        // 仅 return 请求对象会导致 0 笔交易被提交（本轮之前 Submitted: 0 就是此原因）。
        return this.sutAdapter.sendRequests(request);
    }

    _readTx() {
        const d = this.devices[this.submitCount % this.devices.length];
        return {
            contractId: this.contractId,
            contractFunction: 'GetDeviceData',
            contractArguments: [d.id],
            readOnly: true,
        };
    }

    _reportTx() {
        const d = this.devices[this.submitCount % this.devices.length];
        const timestamp = this.baseTimestamp + this.submitCount; // 每笔唯一，避免 data-<device>-<ts> 键冲突
        const sig = this._sign(d, this.dataType, this.value, timestamp);
        return {
            contractId: this.contractId,
            contractFunction: 'ReportData',
            contractArguments: [d.id, this.dataType, this.value, String(timestamp), sig],
            readOnly: false,
        };
    }

    async cleanupWorkloadModule() {
        // 无持久资源需释放
    }
}

function createWorkloadModule() {
    return new IoTDataWorkload();
}

module.exports.createWorkloadModule = createWorkloadModule;
