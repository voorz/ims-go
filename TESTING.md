# 测试风格指南（WS-0）

> D-006：测试以行为、端到端、并发验证为主，禁止依赖脆弱的 restoration/snapshot 对比。

## 强制要求

1. **行为断言**：断言可观察行为（状态迁移、发出的消息、事件），不做内部结构快照对比。
2. **`-race` 必开**：`go test -race -count=1 ./...`；并发场景必须有对应的 race 测试。
3. **fake 对端**：e2e 一律用 fake（fake ePDG、fake P-CSCF 基于 sipgo server），不依赖真实网络。
4. **表驱动**：多用例场景用表测；错误路径必须覆盖。
5. **命名**：`Test<行为>_<场景>`，例如 `TestRegister_ChallengeThenSuccess`。

## 禁止

- restoration/snapshot 风格测试（保存内部结构、回放对比）。
- 测试中拼 raw SIP（测试也要走 sipgo 对象；fake server 侧的断言看解析后的对象）。
- 睡眠等待：用 channel/条件轮询代替 `time.Sleep`（必要时用短超时 + 轮询）。

## 每批次交付

每个 WS 交付时，测试文件与实现一起提交；WS 报告附 `go test -race` 结果。
