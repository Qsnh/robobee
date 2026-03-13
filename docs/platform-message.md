## 飞书消息

1.调用发送消息接口给用户或者群组发送消息

https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/create

参数如下：

```json
{
  "receive_id": "ou_7d8a6e6df7621556ce0d21922b676706ccs",
  "msg_type": "text",
  "content": "{\"text\":\"test content\"}",
  "uuid": "选填，每次调用前请更换，如a0d69e20-1dd1-458b-k525-dfeca4015204"
}
```

2.调用回复消息接口

> 在群聊中会建立消息回复的层级结构

https://open.feishu.cn/document/server-docs/im-v1/message/reply?appId=cli_a93a5f3834badbb6

参数如下：

```json
{
  "content": "{\"text\":\"test content\"}",
  "msg_type": "text",
  "reply_in_thread": true,
  "uuid": "选填，每次调用前请更换，如a0d69e20-1dd1-458b-k525-dfeca4015204"
}
```

## 钉钉消息

方式一：通过 message event 中的 sessionWebhook 发送消息

https://open.dingtalk.com/document/dingstart/robot-reply-and-send-messages

方式二：通过发送消息接口主动推送消息

https://open.dingtalk.com/document/development/the-robot-sends-a-group-message

