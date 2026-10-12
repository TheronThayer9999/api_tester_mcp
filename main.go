// Command apiprobe-mcp exposes HTTP API testing tools over the Model
// Context Protocol (stdio or streamable HTTP) so MCP clients such as
// OpenCode can call and assert on HTTP APIs directly.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

const (
	serverName    = "apiprobe-mcp"
	serverVersion = "1.0.0"
)

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("apiprobe-mcp: ")

	transport := flag.String("transport", envOr("APIPROBE_TRANSPORT", "stdio"), "transport to serve on: stdio|http")
	addr := flag.String("addr", envOr("APIPROBE_ADDR", ":8931"), "listen address for the http transport")
	endpointPath := flag.String("path", envOr("APIPROBE_PATH", "/mcp"), "endpoint path for the http transport")
	flag.Parse()

	s := server.NewMCPServer(serverName, serverVersion)

	s.AddTool(httpRequestTool(), handleHTTPRequest)
	s.AddTool(apiTestTool(), handleAPITest)
	s.AddTool(saveAuthTool(), handleSaveAuth)
	s.AddTool(listAuthTool(), handleListAuth)
	s.AddTool(deleteAuthTool(), handleDeleteAuth)
	s.AddTool(openapiEndpointsTool(), handleOpenAPIEndpoints)

	switch strings.ToLower(strings.TrimSpace(*transport)) {
	case "http":
		httpServer := server.NewStreamableHTTPServer(s, server.WithEndpointPath(*endpointPath))
		log.Printf("serving streamable http on %s%s", *addr, *endpointPath)
		if err := httpServer.Start(*addr); err != nil {
			log.Fatalf("http server: %v", err)
		}
	case "", "stdio":
		if err := server.ServeStdio(s); err != nil {
			fmt.Fprintln(os.Stderr, "apiprobe-mcp:", err)
			os.Exit(1)
		}
	default:
		log.Fatalf("unknown transport %q (want stdio or http)", *transport)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
