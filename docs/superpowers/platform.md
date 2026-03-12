飞书消息结构

> 20260312

```json
{
    "schema": "2.0",
    "header": {
        "event_id": "5e3702a84e847582be8db7fb73283c02",
        "event_type": "im.message.receive_v1",
        "create_time": "1608725989000",
        "token": "rvaYgkND1GOiu5MM0E1rncYC6PLtF7JV",
        "app_id": "cli_9f5343c580712544",
        "tenant_key": "2ca1d211f64f6438"
    },
    "event": {
        "sender": {
            "sender_id": {
                "union_id": "on_8ed6aa67826108097d9ee143816345",
                "user_id": "e33ggbyz",
                "open_id": "ou_84aad35d084aa403a838cf73ee18467"
            },
            "sender_type": "user",
            "tenant_key": "736588c9260f175e"
        },
        "message": {
            "message_id": "om_5ce6d572455d361153b7cb51da133945",
            "root_id": "om_5ce6d572455d361153b7cb5xxfsdfsdfdsf",
            "parent_id": "om_5ce6d572455d361153b7cb5xxfsdfsdfdsf",
            "create_time": "1609073151345",
            "update_time": "1687343654666",
            "chat_id": "oc_5ce6d572455d361153b7xx51da133945",
            "thread_id": "omt_d4be107c616",
            "chat_type": "group",
            "message_type": "text",
            "content": "{\"text\":\"@_user_1 hello\"}",
            "mentions": [
                {
                    "key": "@_user_1",
                    "id": {
                        "union_id": "on_8ed6aa67826108097d9ee143816345",
                        "user_id": "e33ggbyz",
                        "open_id": "ou_84aad35d084aa403a838cf73ee18467"
                    },
                    "name": "Tom",
                    "tenant_key": "736588c9260f175e"
                }
            ],
            "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 13_2_1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.4951.53 Safari/537.36 Lark/6.7.5 LarkLocale/en_US ttnet SDK-Version/6.7.8"
        }
    }
}
```

钉钉消息结构：

> 20260312

```json
{
  "conversationId": "cidjMOsz******AhS4Jg==",
  "atUsers": [
    {
      "dingtalkId": "$:LWCP_v1:$v*********f1+2ow21Zzg"
    }
  ],
  "chatbotCorpId": "ding9f50*****6741",
  "chatbotUserId": "$:LWCP_v1:$v*********1+2ow21Zzg",
  "msgId": "msg2*****p+w==",
  "senderNick": "小钉",
  "isAdmin": true,
  "senderStaffId": "452523285939877041",
  "sessionWebhookExpiredTime": 1695204671648,
  "createAt": 1695199269062,
  "senderCorpId": "ding9f50*****6741",
  "conversationType": "2",
  "senderId": "$:LWCP_v1:$C*********pnNGsP0HS+",
  "conversationTitle": "统一应用模型-测试群",
  "isInAtList": true,
  "sessionWebhook": "https://oapi.dingtalk.com/robot/sendBySession?session=c610ee93e4d96899d9236bd4bea185dd",
  "text": {
    "content": " 你好"
  },
  "robotCode": "ding1f*******dabddc",
  "msgtype": "text"
}
```