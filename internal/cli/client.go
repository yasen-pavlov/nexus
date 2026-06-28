package cli

import "github.com/muty/nexus/internal/cliclient"

// flagJSONUsage is the shared help text for the --json output flag.
const flagJSONUsage = "output raw JSON"

// authedClient loads config, resolves the server + token (flag > env > stored),
// and returns a client ready for authenticated calls. It returns errNotLoggedIn
// when no token is resolvable.
func authedClient(rf *rootFlags) (client *cliclient.Client, server string, err error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, "", err
	}
	server = resolveServerURL(rf.server, cfg)
	token := resolveToken(cfg, server)
	if token == "" {
		return nil, "", errNotLoggedIn
	}
	return cliclient.New(server, token), server, nil
}
