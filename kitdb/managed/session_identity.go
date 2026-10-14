package managed

import "context"

type sessionAccountKey struct{}

// WithSessionAccount carries an authenticated snapshot through root mutation
// revalidation under the root mutex, including after password hashing.
func WithSessionAccount(ctx context.Context, account UserInfo) context.Context {
	return context.WithValue(ctx, sessionAccountKey{}, account)
}

func (root *Root) administratorForContextLocked(ctx context.Context, actor string) error {
	if err := root.administratorLocked(actor); err != nil {
		return err
	}
	if captured, ok := ctx.Value(sessionAccountKey{}).(UserInfo); ok {
		if !captured.Admin {
			return ErrPermission
		}
		for _, u := range root.auth.Users {
			if u.Name == actor && captured.SameAccount(root.stampAccountLocked(u.UserInfo)) {
				return nil
			}
		}
		return ErrPermission
	}
	return nil
}
