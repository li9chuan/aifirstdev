// Package mail defines the public surface shared by the optional mail plugin modules.
package mail

const (
	TopicMceMailGet        = "MceMailGet"
	TopicMseMailGet        = "MseMailGet"
	TopicMceMailList       = "MceMailList"
	TopicMseMailList       = "MseMailList"
	TopicMceMailRead       = "MceMailRead"
	TopicMseMailRead       = "MseMailRead"
	TopicMceMailClaim      = "MceMailClaim"
	TopicMseMailClaim      = "MseMailClaim"
	TopicMceMailDeleteRead = "MceMailDeleteRead"
	TopicMseMailDeleteRead = "MseMailDeleteRead"
	TopicMseMailReceived   = "MseMailReceived"

	TopicMailAdd        = "Mail.Add"
	TopicMailAddBatch   = "Mail.AddBatch"
	TopicMailList       = "Mail.List"
	TopicMailGet        = "Mail.Get"
	TopicMailRead       = "Mail.Read"
	TopicMailClaim      = "Mail.Claim"
	TopicMailDeleteRead = "Mail.DeleteRead"
)

const (
	RetOK       int32 = 0
	RetInvalid  int32 = 1
	RetNotFound int32 = 2
	RetInternal int32 = 3
	RetPartial  int32 = 4
	RetBusy     int32 = 5
	RetTimeout  int32 = 6
)
