接收到客户端消息
Handler 接收和处理请求

具体消息如何处理需要通过对消息的类型分类后处理  
1.普通消息  
2.配置更新消息


消息的发送涉及到转发的过程如果当前节点不是共识中的Leader节点，需要将这个请求消息转发到leader

创建Stream,在流上控制消息的发送处理和响应



文件名为broadcast 实际上就是将请求转发到对应通道的Leader进行处理