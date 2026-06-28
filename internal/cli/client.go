package cli

import "github.com/muty/nexus/internal/cliclient"

// flagJSONUsage is the shared help text for the --json output flag.
const flagJSONUsage = "output raw JSON"

// resolveClient loads config and resolves the server + token (flag > env >
// stored), ALWAYS returning a client — even when no token resolves, in which
// case the client is unauthenticated (Authenticated() == false). It never
// returns errNotLoggedIn: the MCP server uses this so it can start regardless of
// auth and surface a clear "not authenticated" tool result, rather than exiting
// and leaving the host with an opaque connection failure.
func resolveClient(rf *rootFlags) (client *cliclient.Client, server string, err error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, "", err
	}
	server = resolveServerURL(rf.server, cfg)
	token := resolveToken(cfg, server)
	return cliclient.New(server, token), server, nil
}

// authedClient resolves a client and requires a credential, returning
// errNotLoggedIn when none is resolvable. Used by every command except `mcp`,
// which tolerates the unauthenticated case via resolveClient.
func authedClient(rf *rootFlags) (client *cliclient.Client, server string, err error) {
	client, server, err = resolveClient(rf)
	if err != nil {
		return nil, "", err
	}
	if !client.Authenticated() {
		return nil, "", errNotLoggedIn
	}
	return client, server, nil
}
