package agent

import "context"

type conversationKey struct{}

// WithConversation binds a request to its host conversation. The MCP boundary
// supplies this identity from request metadata, never from task arguments.
func WithConversation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, conversationKey{}, id)
}

func Conversation(ctx context.Context) string {
	id, _ := ctx.Value(conversationKey{}).(string)
	return id
}

// SessionTaskRuntime can start a new task in an existing durable session. An
// empty sessionID creates the conversation's first session. Each task still
// receives its own control token, routing and capability snapshot.
type SessionTaskRuntime interface {
	StartTaskInSession(ctx context.Context, task AgentTask, name, model, sessionID string) (RuntimeHandle, error)
}
