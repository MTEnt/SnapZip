package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MTEnt/SnapZip/core"
	"github.com/klauspost/compress/zstd"
)

const mcpProtocolVersion = "2025-06-18"

var supportedMCPProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

type mcpServer struct {
	dbDir string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content           []mcpContent `json:"content"`
	StructuredContent any          `json:"structuredContent,omitempty"`
	IsError           bool         `json:"isError,omitempty"`
}

func handleMCP() {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	dbDir := fs.String("db-dir", ".", "Directory of memory.db")
	_ = fs.Parse(os.Args[2:])

	if err := runMCPServer(os.Stdin, os.Stdout, *dbDir); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server failed: %v\n", err)
		os.Exit(1)
	}
}

func runMCPServer(input io.Reader, output io.Writer, dbDir string) error {
	server := mcpServer{dbDir: dbDir}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	writer := bufio.NewWriter(output)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			fmt.Fprintf(os.Stderr, "Ignoring invalid MCP JSON-RPC message: %v\n", err)
			continue
		}

		response, ok := server.handleRequest(req)
		if !ok {
			continue
		}
		if err := writeRPCResponse(writer, response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s mcpServer) handleRequest(req rpcRequest) (rpcResponse, bool) {
	if len(req.ID) == 0 && strings.HasPrefix(req.Method, "notifications/") {
		return rpcResponse{}, false
	}

	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.initializeResult(req.Params),
		}, len(req.ID) > 0
	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}, len(req.ID) > 0
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": s.tools()},
		}, len(req.ID) > 0
	case "tools/call":
		result, rpcErr := s.callTool(req.Params)
		if rpcErr != nil {
			return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, len(req.ID) > 0
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, len(req.ID) > 0
	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "Method not found: " + req.Method},
		}, len(req.ID) > 0
	}
}

func (s mcpServer) initializeResult(params json.RawMessage) map[string]any {
	version := mcpProtocolVersion
	var initParams struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 && json.Unmarshal(params, &initParams) == nil && supportedMCPProtocolVersions[initParams.ProtocolVersion] {
		version = initParams.ProtocolVersion
	}

	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":        "snapzip",
			"title":       "SnapZip",
			"version":     "0.1.0",
			"description": "Local codebase memory and context packs for AI coding agents.",
		},
		"instructions": "Use SnapZip tools to search indexed local code, build bounded context packs, inspect repo maps and symbols, read feedback memory, and inspect index stats. Tools are read-only.",
	}
}

func (s mcpServer) tools() []mcpTool {
	return []mcpTool{
		{
			Name:        "search",
			Title:       "Search SnapZip memory",
			Description: "Search indexed local code snippets using SnapZip relevance ranking.",
			InputSchema: objectSchema(
				[]string{"query"},
				map[string]any{
					"query":  stringSchema("Search query, such as a feature, symbol, file, or implementation topic."),
					"db_dir": stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
					"limit":  integerSchema("Maximum snippets to return.", 1, 100),
				},
			),
		},
		{
			Name:        "context_pack",
			Title:       "Build SnapZip context pack",
			Description: "Build a bounded Markdown context pack with relevant snippets and feedback memory.",
			InputSchema: objectSchema(
				[]string{"query"},
				map[string]any{
					"query":          stringSchema("Search query for the context pack."),
					"db_dir":         stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
					"limit":          integerSchema("Maximum snippets to consider.", 1, 100),
					"budget":         integerSchema("Approximate byte budget for rendered context.", core.MinContextPackBudgetBytes, core.MaxContextPackBudgetBytes),
					"feedback_limit": integerSchema("Maximum feedback entries to include.", 0, 100),
					"mode":           stringSchema("Optional pack mode: debug, refactor, test, or docs."),
				},
			),
		},
		{
			Name:        "map",
			Title:       "Show SnapZip repo map",
			Description: "Return a compact repo map from indexed symbols.",
			InputSchema: objectSchema(
				nil,
				map[string]any{
					"db_dir": stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
					"limit":  integerSchema("Maximum files to include.", 1, 100),
				},
			),
		},
		{
			Name:        "symbols",
			Title:       "Search SnapZip symbols",
			Description: "Search indexed functions, classes, methods, and types.",
			InputSchema: objectSchema(
				[]string{"query"},
				map[string]any{
					"query":  stringSchema("Symbol, file, language, or signature query."),
					"db_dir": stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
					"limit":  integerSchema("Maximum symbols to return.", 1, 100),
				},
			),
		},
		{
			Name:        "related",
			Title:       "Find related files",
			Description: "Find files related to an indexed source path using shared indexed symbols.",
			InputSchema: objectSchema(
				[]string{"path"},
				map[string]any{
					"path":   stringSchema("Indexed source path, such as core/database.go."),
					"db_dir": stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
					"limit":  integerSchema("Maximum related files to return.", 1, 100),
				},
			),
		},
		{
			Name:        "get_feedback",
			Title:       "Read SnapZip feedback",
			Description: "Read recent local negative feedback memory.",
			InputSchema: objectSchema(
				nil,
				map[string]any{
					"db_dir": stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
					"limit":  integerSchema("Maximum feedback entries to return.", 1, 100),
				},
			),
		},
		{
			Name:        "stats",
			Title:       "Inspect SnapZip stats",
			Description: "Show indexed row counts and language breakdown for the local memory database.",
			InputSchema: objectSchema(
				nil,
				map[string]any{
					"db_dir": stringSchema("Directory containing memory.db. Defaults to the server --db-dir."),
				},
			),
		},
	}
}

func (s mcpServer) callTool(params json.RawMessage) (mcpToolResult, *rpcError) {
	var call mcpToolCall
	if len(params) == 0 {
		return mcpToolResult{}, &rpcError{Code: -32602, Message: "tools/call params are required"}
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return mcpToolResult{}, &rpcError{Code: -32602, Message: "invalid tools/call params: " + err.Error()}
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}

	switch call.Name {
	case "search":
		return s.callSearch(call.Arguments), nil
	case "context_pack":
		return s.callContextPack(call.Arguments), nil
	case "map":
		return s.callMap(call.Arguments), nil
	case "symbols":
		return s.callSymbols(call.Arguments), nil
	case "related":
		return s.callRelated(call.Arguments), nil
	case "get_feedback":
		return s.callGetFeedback(call.Arguments), nil
	case "stats":
		return s.callStats(call.Arguments), nil
	default:
		return mcpToolResult{}, &rpcError{Code: -32602, Message: "Unknown tool: " + call.Name}
	}
}

func (s mcpServer) callSearch(args map[string]any) mcpToolResult {
	query := strings.TrimSpace(stringArg(args, "query", ""))
	if query == "" {
		return toolError("query is required")
	}

	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	comp, err := core.NewZstdCompressor(zstd.SpeedDefault)
	if err != nil {
		return toolError(err.Error())
	}

	result, err := core.SearchMemory(db, comp, query, intArg(args, "limit", 3), 5)
	if err != nil {
		return toolError(err.Error())
	}
	return toolSuccess(core.RenderSearchResult(result), result)
}

func (s mcpServer) callContextPack(args map[string]any) mcpToolResult {
	query := strings.TrimSpace(stringArg(args, "query", ""))
	if query == "" {
		return toolError("query is required")
	}

	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	comp, err := core.NewZstdCompressor(zstd.SpeedDefault)
	if err != nil {
		return toolError(err.Error())
	}

	pack, err := core.BuildContextPackWithMode(
		db,
		comp,
		query,
		stringArg(args, "mode", ""),
		intArg(args, "limit", 5),
		intArg(args, "budget", core.DefaultContextPackBudgetBytes),
		intArg(args, "feedback_limit", 5),
	)
	if err != nil {
		return toolError(err.Error())
	}
	return toolSuccess(core.RenderContextPack(pack), pack)
}

func (s mcpServer) callMap(args map[string]any) mcpToolResult {
	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	repoMap, err := core.BuildRepoMap(db, intArg(args, "limit", 50))
	if err != nil {
		return toolError(err.Error())
	}
	return toolSuccess(core.RenderRepoMap(repoMap), repoMap)
}

func (s mcpServer) callSymbols(args map[string]any) mcpToolResult {
	query := strings.TrimSpace(stringArg(args, "query", ""))
	if query == "" {
		return toolError("query is required")
	}
	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	symbols, err := core.SearchSymbols(db, query, intArg(args, "limit", 20))
	if err != nil {
		return toolError(err.Error())
	}
	var builder strings.Builder
	for _, symbol := range symbols {
		fmt.Fprintf(&builder, "%s:%d [%s %s] %s\n", symbol.Path, symbol.Line, symbol.Language, symbol.Kind, symbol.Signature)
	}
	return toolSuccess(builder.String(), symbols)
}

func (s mcpServer) callRelated(args map[string]any) mcpToolResult {
	path := strings.TrimSpace(stringArg(args, "path", ""))
	if path == "" {
		return toolError("path is required")
	}
	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	files, err := core.RelatedFiles(db, path, intArg(args, "limit", 10))
	if err != nil {
		return toolError(err.Error())
	}
	repoMap := core.RepoMap{Files: files}
	return toolSuccess(core.RenderRepoMap(repoMap), files)
}

func (s mcpServer) callGetFeedback(args map[string]any) mcpToolResult {
	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	feedback, err := core.RetrieveNegativeFeedback(db, intArg(args, "limit", 10))
	if err != nil {
		return toolError(err.Error())
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Found %d negative feedback entries in memory.db:\n", len(feedback))
	for _, entry := range feedback {
		fmt.Fprintf(&builder, "\n[%s] Sentiment: %q\nUser Feedback: %q\nBot Output: %q\n", entry.CreatedAt, entry.Sentiment, entry.UserInput, entry.BotResponse)
	}
	return toolSuccess(builder.String(), feedback)
}

func (s mcpServer) callStats(args map[string]any) mcpToolResult {
	db, done, err := s.openDB(args)
	if err != nil {
		return toolError(err.Error())
	}
	defer done()

	stats, err := core.GetDatabaseStats(db)
	if err != nil {
		return toolError(err.Error())
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "knowledge rows: %d\n", stats.KnowledgeRows)
	fmt.Fprintf(&builder, "feedback rows: %d\n", stats.FeedbackRows)
	if len(stats.Languages) == 0 {
		builder.WriteString("languages: none\n")
	} else {
		builder.WriteString("languages:\n")
		for _, lang := range stats.Languages {
			fmt.Fprintf(&builder, "  %s: %d\n", lang.Language, lang.Count)
		}
	}
	return toolSuccess(builder.String(), stats)
}

func (s mcpServer) openDB(args map[string]any) (*sql.DB, func(), error) {
	dbDir := stringArg(args, "db_dir", s.dbDir)
	db, err := core.InitDB(dbDir)
	if err != nil {
		return nil, func() {}, err
	}
	return db, func() { _ = db.Close() }, nil
}

func toolSuccess(text string, structured any) mcpToolResult {
	return mcpToolResult{
		Content:           []mcpContent{{Type: "text", Text: text}},
		StructuredContent: structured,
	}
}

func toolError(message string) mcpToolResult {
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: message}},
		IsError: true,
	}
}

func writeRPCResponse(writer *bufio.Writer, response rpcResponse) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := writer.Write(payload); err != nil {
		return err
	}
	if err := writer.WriteByte('\n'); err != nil {
		return err
	}
	return writer.Flush()
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func integerSchema(description string, minimum int, maximum int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     minimum,
		"maximum":     maximum,
	}
}

func stringArg(args map[string]any, name string, fallback string) string {
	value, ok := args[name]
	if !ok || value == nil {
		return fallback
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fallback
}

func intArg(args map[string]any, name string, fallback int) int {
	value, ok := args[name]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return fallback
}
