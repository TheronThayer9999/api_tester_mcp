package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

const defaultAPIKeyHeader = "X-API-Key"

// auth is the process-wide store of auth profiles, shared by the tools.
var auth = newAuthStore()

type authProfile struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	Header    string `json:"header,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	AppliesTo string `json:"applies_to,omitempty"`
}

func (p authProfile) apply(req *http.Request) {
	switch p.Type {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+p.Token)
	case "basic":
		req.SetBasicAuth(p.Username, p.Password)
	case "api_key":
		header := p.Header
		if header == "" {
			header = defaultAPIKeyHeader
		}
		req.Header.Set(header, p.Token)
	}
}

func (p authProfile) matches(rawURL string) bool {
	if p.AppliesTo == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(rawURL), strings.ToLower(p.AppliesTo))
}

type authStore struct {
	mu       sync.RWMutex
	profiles map[string]authProfile
}

func newAuthStore() *authStore {
	return &authStore{profiles: map[string]authProfile{}}
}

func (s *authStore) save(p authProfile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[p.Name] = p
}

func (s *authStore) get(name string) (authProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[name]
	return p, ok
}

func (s *authStore) delete(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[name]; !ok {
		return false
	}
	delete(s.profiles, name)
	return true
}

func (s *authStore) list() []authProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]authProfile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b authProfile) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// resolve resolves the profiles to attach: an explicit name wins, otherwise
// every profile whose applies_to prefix matches the request URL.
func (s *authStore) resolve(name, rawURL string) ([]authProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if name != "" {
		p, ok := s.profiles[name]
		if !ok {
			return nil, fmt.Errorf("auth profile %q not found", name)
		}
		return []authProfile{p}, nil
	}
	var out []authProfile
	for _, p := range s.profiles {
		if p.matches(rawURL) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b authProfile) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// apply attaches the profile headers to req and returns the applied names.
func (s *authStore) apply(req *http.Request, name string) ([]string, error) {
	profiles, err := s.resolve(name, req.URL.String())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		p.apply(req)
		names = append(names, p.Name)
	}
	return names, nil
}

type saveAuthInput struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Token     string `json:"token"`
	Header    string `json:"header"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	AppliesTo string `json:"applies_to"`
}

type authListOutput struct {
	Count    int           `json:"count"`
	Profiles []authProfile `json:"profiles"`
}

type deleteAuthInput struct {
	Name string `json:"name"`
}

func saveAuthTool() mcp.Tool {
	return mcp.NewTool("save_auth",
		mcp.WithDescription("Save a reusable auth profile (bearer token, basic auth, or API key) so later http_request/api_test calls attach it automatically. Profiles live in memory for this server session and are auto-applied to requests whose URL starts with applies_to."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Profile name to reference later, e.g. \"prod\""),
		),
		mcp.WithString("type",
			mcp.Required(),
			mcp.Description("Auth mechanism"),
			mcp.Enum("bearer", "basic", "api_key"),
		),
		mcp.WithString("token",
			mcp.Description("Secret value for bearer or api_key auth"),
		),
		mcp.WithString("header",
			mcp.Description("Header name for api_key auth (default X-API-Key)"),
		),
		mcp.WithString("username",
			mcp.Description("Username for basic auth"),
		),
		mcp.WithString("password",
			mcp.Description("Password for basic auth"),
		),
		mcp.WithString("applies_to",
			mcp.Description("URL prefix the profile auto-applies to, e.g. https://api.example.com (omit to apply to every request)"),
		),
	)
}

func handleSaveAuth(_ context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in saveAuthInput
	if err := r.BindArguments(&in); err != nil {
		return mcp.NewToolResultErrorFromErr("invalid arguments", err), nil
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.ToLower(strings.TrimSpace(in.Type))
	if in.Name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	switch in.Type {
	case "bearer", "api_key":
		if strings.TrimSpace(in.Token) == "" {
			return mcp.NewToolResultErrorf("%s auth requires token", in.Type), nil
		}
	case "basic":
		if strings.TrimSpace(in.Username) == "" && strings.TrimSpace(in.Password) == "" {
			return mcp.NewToolResultError("basic auth requires username or password"), nil
		}
	default:
		return mcp.NewToolResultErrorf("unknown auth type %q (want bearer, basic, or api_key)", in.Type), nil
	}

	profile := authProfile{
		Name:      in.Name,
		Type:      in.Type,
		Token:     in.Token,
		Header:    in.Header,
		Username:  in.Username,
		Password:  in.Password,
		AppliesTo: strings.TrimSpace(in.AppliesTo),
	}
	auth.save(profile)

	out := map[string]any{
		"saved":       true,
		"name":        profile.Name,
		"type":        profile.Type,
		"applies_to":  profile.AppliesTo,
		"auto_apply":  profile.AppliesTo != "",
		"token_redacted": redactSecret(profile.Token),
	}
	return jsonResult(out)
}

func listAuthTool() mcp.Tool {
	return mcp.NewTool("list_auth",
		mcp.WithDescription("List saved auth profiles with redacted secrets."),
	)
}

func handleListAuth(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	profiles := auth.list()
	out := authListOutput{Count: len(profiles), Profiles: []authProfile{}}
	for _, p := range profiles {
		if p.Token != "" {
			p.Token = redactSecret(p.Token)
		}
		if p.Password != "" {
			p.Password = redactSecret(p.Password)
		}
		out.Profiles = append(out.Profiles, p)
	}
	return jsonResult(out)
}

func deleteAuthTool() mcp.Tool {
	return mcp.NewTool("delete_auth",
		mcp.WithDescription("Delete a saved auth profile by name."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Profile name to delete"),
		),
	)
}

func handleDeleteAuth(_ context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in deleteAuthInput
	if err := r.BindArguments(&in); err != nil {
		return mcp.NewToolResultErrorFromErr("invalid arguments", err), nil
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	deleted := auth.delete(in.Name)
	return jsonResult(map[string]any{"name": in.Name, "deleted": deleted})
}

func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "…(redacted)"
}
