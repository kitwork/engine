package request

import (
	"context"
	"sort"
)

// Principal is the authenticated subject attached by trusted host middleware.
// Attributes are copied at the request boundary and are immutable afterwards.
type Principal struct {
	Subject       string
	Authenticated bool
	Attributes    map[string]string
}

// Authorization combines one principal with its granted capability names.
type Authorization struct {
	Principal   Principal
	Permissions []string
}

type authorizationContextKey struct{}

// WithAuthorization is the trusted host seam for attaching authentication to
// an HTTP request context before the Kitwork request scope is created.
func WithAuthorization(ctx context.Context, authorization Authorization) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, authorizationContextKey{}, cloneAuthorization(authorization))
}

func authorizationFromContext(ctx context.Context) Authorization {
	if ctx == nil {
		return Authorization{}
	}
	authorization, _ := ctx.Value(authorizationContextKey{}).(Authorization)
	return cloneAuthorization(authorization)
}

func cloneAuthorization(source Authorization) Authorization {
	attributes := make(map[string]string, len(source.Principal.Attributes))
	for key, current := range source.Principal.Attributes {
		attributes[key] = current
	}
	permissions := append([]string(nil), source.Permissions...)
	sort.Strings(permissions)
	return Authorization{
		Principal: Principal{
			Subject:       source.Principal.Subject,
			Authenticated: source.Principal.Authenticated,
			Attributes:    attributes,
		},
		Permissions: permissions,
	}
}
