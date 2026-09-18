package lark

const (
	apiMessagesPath = "/open-apis/im/v1/messages/"
	apiMessagesQueryPrefix = "/open-apis/im/v1/messages?"
	errFmtResourceExceeds = "lark http client: download resource: resource exceeds %d bytes"
	errFmtWriteAck = "write ack: %w"
	errMsgMissingChatID = "lark http client: missing chat_id"
	errMsgMissingMessageID = "lark http client: missing message_id"
	formatSenderPrefix = "[%s]: %s"
	textEmptyMessage = "[empty message]"
)
