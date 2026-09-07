package authbridge

import "context"

type User struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

// UserResolver maps an authenticated token subject to Mailflow's canonical user.
// It deliberately uses context.Context so application modules stay independent
// from the HTTP transport.
type UserResolver interface {
	FindBySubject(context.Context, string) (User, error)
}

type userContextKey struct{}

func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey{}).(User)
	return user, ok
}
