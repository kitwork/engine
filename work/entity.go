package work

type Entity struct {
	Identity string
	Domain   string
}

func NewEntity(identity string, domain string) *Entity {
	return &Entity{
		Identity: identity,
		// Domain:   domain,
	}
}
