# 更新记录

本文件记录项目的所有重要变更，格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.1/)，按提交时间倒序归类，
每个时间点对应一次提交（对应的提交信息可用 `git log` 查看）。

## [未发布]

### 变更

- 从站 `Handler` 接口的 8 个方法增加 `unitID` 参数：`ServerConfig.UnitID` 为 0（应答任意地址）时，
  处理器可以区分每个请求来自哪个从站地址（RTU 一条总线多台设备时用它查表）
- `MaskWriteHandler` / `ReadWriteHandler` 两个可选接口同步增加 `unitID` 参数
- `DataModel` 的方法签名同步调整（忽略 `unitID`，仍是单块数据区；应用侧直接读取时传 0 即可）
- `Server.Serve` 在传输层断开（串口拔出、TCP 对端关闭）时返回错误，不再空转
- `example` 增加 Modbus TCP：主站 `-host`，从站 `-listen`
- `example/slave` 的持久化示例改为按 `unitID` 区分设备（SQL 里带上 `unit` 条件）
- 明确从站定位为「服务端」：文档与 `example/slave` 按“真实主站读写本程序的数据与开关”描述，
  示例新增 `applyCoils` 演示收到开关指令后执行业务动作

### 移除

- 未使用的内部函数 `errorf` 与测试辅助函数 `rtuReply`

## [2026-09-16 15:58] 添加modbus slave；添加modbus TCP

### 新增

- Modbus 从站
- Modbus TCP
- 示例：Modbus 从站

### 文档

- 添加使用说明：Modbus 从站

## [2026-09-16 10:49] 实现Modbus主站

### 新增

- Modbus 数据处理
- Modbus 主站
- Modbus RTU/ASCII 双模式
- 返回的错误信息支持 en/zh 多语言
- 示例：Modbus 主站

### 变更

- 修改包名

### 移除

- 清理模板中不需要的代码

## [2026-09-15 17:29] Initial commit

### 新增

- 仓库初始化
