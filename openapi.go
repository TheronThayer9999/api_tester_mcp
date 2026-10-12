package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mark3labs/mcp-go/mcp"
)

const defaultEndpointLimit = 100

type openapiInput struct {
	Source       string `json:"source"`
	MethodFilter string `json:"method_filter"`
	PathFilter   string `json:"path_filter"`
	Limit        int    `json:"limit"`
}

type endpointInfo struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	OperationID string   `json:"operation_id,omitempty"`
	BaseURL     string   `json:"base_url,omitempty"`
	FullURL     string   `json:"full_url,omitempty"`
	Parameters  []string `json:"parameters,omitempty"`
}

type openapiOutput struct {
	Source    string        `json:"source"`
	Title     string        `json:"title,omitempty"`
	Version   string        `json:"version,omitempty"`
	Count     int           `json:"count"`
	Endpoints []endpointInfo `json:"endpoints"`
}

func openapiEndpointsTool() mcp.Tool {
	return mcp.NewTool("openapi_endpoints",
		mcp.WithDescription("List the operations of an OpenAPI/Swagger spec so HTTP calls can be aimed at real endpoints. Loads JSON or YAML from an http(s) URL or a local file path."),
		mcp.WithString("source",
			mcp.Required(),
			mcp.Description("OpenAPI spec location: http(s) URL or local file path"),
		),
		mcp.WithString("method_filter",
			mcp.Description("Only list operations with this HTTP method, e.g. GET"),
		),
		mcp.WithString("path_filter",
			mcp.Description("Only list operations whose path contains this substring"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of endpoints to return (default 100, max 500)"),
		),
	)
}

func handleOpenAPIEndpoints(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in openapiInput
	if err := r.BindArguments(&in); err != nil {
		return mcp.NewToolResultErrorFromErr("invalid arguments", err), nil
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		return mcp.NewToolResultError("source is required"), nil
	}

	doc, err := loadSpec(source)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to load spec", err), nil
	}

	out := openapiOutput{Source: source, Endpoints: []endpointInfo{}}
	if doc.Info != nil {
		out.Title = doc.Info.Title
		out.Version = doc.Info.Version
	}
	baseURL := ""
	if len(doc.Servers) > 0 && doc.Servers[0] != nil {
		baseURL = strings.TrimSuffix(doc.Servers[0].URL, "/")
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultEndpointLimit
	}
	if limit > 500 {
		limit = 500
	}
	methodFilter := strings.ToUpper(strings.TrimSpace(in.MethodFilter))
	pathFilter := strings.ToLower(strings.TrimSpace(in.PathFilter))

	paths := doc.Paths
	if paths != nil {
		items := paths.Map()
		keys := make([]string, 0, len(items))
		for key := range items {
			keys = append(keys, key)
		}
		slices.Sort(keys)

	outer:
		for _, path := range keys {
			if pathFilter != "" && !strings.Contains(strings.ToLower(path), pathFilter) {
				continue
			}
			for method, op := range items[path].Operations() {
				if methodFilter != "" && strings.ToUpper(method) != methodFilter {
					continue
				}
				info := endpointInfo{
					Method:  strings.ToUpper(method),
					Path:    path,
					BaseURL: baseURL,
				}
				if baseURL != "" {
					info.FullURL = baseURL + path
				}
				if op != nil {
					info.Summary = op.Summary
					info.OperationID = op.OperationID
					for _, ref := range op.Parameters {
						if ref == nil || ref.Value == nil {
							continue
						}
						p := ref.Value
						desc := p.Name + " (" + string(p.In)
						if p.Required {
							desc += ", required"
						}
						desc += ")"
						info.Parameters = append(info.Parameters, desc)
					}
				}
				out.Endpoints = append(out.Endpoints, info)
				if len(out.Endpoints) >= limit {
					break outer
				}
			}
		}
	}

	out.Count = len(out.Endpoints)
	return jsonResult(out)
}

func loadSpec(source string) (*openapi3.T, error) {
	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return openapi3.NewLoader().LoadFromURI(u)
	}
	if _, err := os.Stat(source); err != nil {
		return nil, fmt.Errorf("cannot read spec %q (want an http(s) URL or an existing file): %w", source, err)
	}
	return openapi3.NewLoader().LoadFromFile(source)
}
