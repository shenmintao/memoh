# 通用消息推送

在 **个人资料 → 消息推送 → 管理接口** 为外部设备或服务创建接口。
选择有管理权限的 Bot 和自己已经关联的渠道账号；先从该账号给所选 Bot
发一条私聊消息，以建立可靠的收件地址。任何 Bot 都可使用，无需模型、MCP
或本地 Runtime 参与，服务器直接调用已有渠道发送服务。

## 鉴权与接入

创建或重置时会显示一次完整地址和密钥。服务器仅存储 SHA-256 哈希。
一个密钥只授权一个固定接口，不能覆盖 Bot、渠道或收件人。
管理接口仍使用登录会话；调用方可以使用以下任一方式：

- `POST /api/push/<接口 ID>`，请求头 `Authorization: Bearer <接口密钥>`。
- `POST /api/push/<接口 ID>?key=<接口密钥>`，适用于不能自定义鉴权头的设备。

使用 HTTPS。内置 Web 反向代理不记录此路径的访问日志；Server 不记录此路径的
查询参数。若在外层另加反向代理，也应避免记录含密钥的 URL。
停用、删除和重置密钥对后续请求生效；已进入渠道的发送可能仍会完成。
取消账号关联、停用账户、撤销 Bot 管理权限或关闭渠道后，不再接受新的发送。

### 通用 JSON

`Content-Type: application/json`，`text`、`content`、`message` 三选一：

```json
{
  "title": "NAS 备份",
  "text": "今天的备份已完成。",
  "event_id": "backup-20260911"
}
```

也支持 `Content-Type: text/plain; charset=utf-8`，直接以正文作为通知内容。
始终作为普通文本发送，不执行通知中的指令。请求体最大 64 KiB，格式化后的
文本最大 16 KiB。每个接口每秒一条，允许短时突发十条，超限返回 429。

### sms_forwarding

已对照 [chenxuuu/sms_forwarding 的推送实现](https://github.com/chenxuuu/sms_forwarding/blob/master/code/push.cpp)。
原项目 POST JSON 会处理引号、反斜杠和换行。修改版仍应以设备实际行为为准。

优先选 **POST JSON**，仅填写 Memoh 生成的地址即可：

```json
{"sender":"10086","message":"短信正文","timestamp":"2026-09-11 22:00:00"}
```

需要设备备注或接收号码时，可选 **POST 灵活模式**，Accept 与 Content-Type
都设为 `application/json`，模板如下。若固件未实现 JSON 转义，应退回标准
POST JSON，而非拼接未转义的短信：

```json
{
  "sender": "{sender}",
  "message": "{message}",
  "timestamp": "{timestamp}",
  "local_number": "{local_number}",
  "remark": "{remark}"
}
```

## 发送结果和重试

- 200：渠道接受了消息；不是用户已读回执。`duplicate: true` 表示同一事件
  已发送，本次没有再次调用渠道。
- 400/413：格式错误或过大；401：密钥无效；403：停用或权限失效；409：
  尚无对应私聊、事件编号内容冲突，或发送仍在进行/结果待确认。
- 429：限流；502：渠道未确认发送；503：存储或推送服务不可用。

通用通知通过 `event_id` 或 `Idempotency-Key` 请求头去重；优先采用请求头。
同一事件重试保持同一个编号，新事件使用新编号。不提供编号时，每次视为
独立通知。标准短信同时含 sender 和 timestamp 时自动按完整通知生成编号。
记录最长保留 30 天，在该接口后续发送时清理；管理页展示最近 20 次。
只保留状态、哈希、时间和错误类别，不保留通知正文。

失败后由调用方决定重试。Server 不把待发内容存入后台队列；上游短信转发器
有失败重试逻辑。如果进程退出导致发送结果待确认，同一编号不会自动重发，
应先检查接收端和记录，再决定是否用新编号重放。渠道可能在超时或分段发送
失败前已接受部分内容，因此不能保证跨平台严格“恰好一次”。

## 微信

微信上下文按渠道配置、登录账号和收件人保存；更换登录账号不会复用旧账号
上下文。与 [Tencent 当前发送实现](https://github.com/Tencent/openclaw-weixin/blob/main/src/messaging/send.ts)
一致，文本发送优先带可用上下文，缺少时交由平台判断是否允许发送。
成功响应可能只有 `message_id`，也可能包含 `ret` / `errcode`；非零业务错误
不会再被当作成功。具体账号的主动发送能力、限额和有效性以平台实际响应为准。
