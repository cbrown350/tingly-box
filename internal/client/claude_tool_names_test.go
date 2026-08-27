package client

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTitleCaseToolName(t *testing.T) {
	// Purely mechanical: the MCP-namespace exemption lives in
	// claudeCodeToolName, not here.
	cases := map[string]string{
		"read_file":     "ReadFile",
		"ha_get_state":  "HaGetState",
		"terminal":      "Terminal",
		"browser_exec":  "BrowserExec",
		"Bash":          "Bash", // already TitleCase
		"WebFetch":      "WebFetch",
		"":              "",
		"mcp_foo_bar":   "McpFooBar",
		"mcp_":          "Mcp",
		"tool_2_thing":  "Tool2Thing",
		"__weird__name": "WeirdName",
	}
	for in, want := range cases {
		assert.Equal(t, want, titleCaseToolName(in), "titleCaseToolName(%q)", in)
	}
}

func TestClaudeCodeToolName_MapWinsOverMechanical(t *testing.T) {
	// The official spelling must win: mechanical folding would give "Ls".
	assert.Equal(t, "LS", claudeCodeToolName("ls"))
	assert.Equal(t, "TodoWrite", claudeCodeToolName("todowrite"))
	assert.Equal(t, "NotebookEdit", claudeCodeToolName("notebookedit"))
	// Not in the map — mechanical fold.
	assert.Equal(t, "SearchFiles", claudeCodeToolName("search_files"))
}

func TestPlanToolRenames_SkipsCollisions(t *testing.T) {
	t.Run("target already taken by another tool", func(t *testing.T) {
		plan := planToolRenames([]string{"my_tool", "MyTool"})
		assert.Empty(t, plan)
	})

	t.Run("two sources folding to the same target", func(t *testing.T) {
		// "foo_bar" and "foo_Bar" both fold to "FooBar"; only the first may rename.
		plan := planToolRenames([]string{"foo_bar", "foo_Bar"})
		assert.Len(t, plan, 1)
		assert.Equal(t, "FooBar", plan["foo_bar"])
	})

	t.Run("independent names all rename", func(t *testing.T) {
		plan := planToolRenames([]string{"read_file", "write_file"})
		assert.Equal(t, map[string]string{
			"read_file":  "ReadFile",
			"write_file": "WriteFile",
		}, plan)
	})
}

// ===================================================================
// MCP-namespaced tool names
//
// Claude Code sends MCP tools verbatim as mcp__<server>__<tool> — lowercase,
// unfolded — so folding them would move the request *away* from real Claude
// Code traffic, not towards it. Tingly-Box's own server tools use the same
// double-underscore convention via internal/tool.NormalizeToolName.
// ===================================================================

func TestClaudeCodeToolName_LeavesMCPNamespacedNamesAlone(t *testing.T) {
	// Real shapes: what an MCP client actually puts on the wire.
	for _, name := range []string{
		"mcp__github__get_pull_request",
		"mcp__linear__create_issue",
		"mcp__read_file",
		"tingly_box_mcp__webtools__mcp_web_search",
		"tingly_box_mcp__advisor__advisor",
		"tingly_box_mcp__brave-search__brave_web_search",
		"tingly_box_mcp__DICOM__view-dicom",
	} {
		assert.Equal(t, name, claudeCodeToolName(name), "claudeCodeToolName(%q) must pass through", name)
	}
}

func TestClaudeCodeToolName_FoldsSingleUnderscoreMcpNames(t *testing.T) {
	// A single-underscore "mcp_" prefix is not an MCP namespace — it is itself
	// a third-party marker. Fold it like any other snake_case name rather than
	// half-folding it into a shape no client emits.
	assert.Equal(t, "McpFooBar", claudeCodeToolName("mcp_foo_bar"))
	assert.Equal(t, "McpLinearGetIssue", claudeCodeToolName("mcp_linear_get_issue"))
	assert.Equal(t, "Mcp", claudeCodeToolName("mcp_"))
}

func TestPlanToolRenames_ExcludesMCPNamespacedNames(t *testing.T) {
	plan := planToolRenames([]string{
		"mcp__github__get_pull_request",
		"tingly_box_mcp__webtools__mcp_web_search",
		"read_file",
	})
	assert.Equal(t, map[string]string{"read_file": "ReadFile"}, plan)
}

// ===================================================================
// tool_choice must follow the same rename plan as tools
// ===================================================================

func v1Tool(name string) anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{Name: name}}
}

func betaTool(name string) anthropic.BetaToolUnionParam {
	return anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{Name: name}}
}

func TestRemapRequestToolNames_RemapsToolChoice(t *testing.T) {
	t.Run("pinned tool follows the fold", func(t *testing.T) {
		req := &anthropic.MessageNewParams{
			Tools:      []anthropic.ToolUnionParam{v1Tool("read_file")},
			ToolChoice: anthropic.ToolChoiceParamOfTool("read_file"),
		}
		rev := remapRequestToolNames(req)
		assert.Equal(t, "ReadFile", req.Tools[0].OfTool.Name)
		assert.Equal(t, "ReadFile", req.ToolChoice.OfTool.Name,
			"tool_choice must name a tool that exists in tools[]")
		assert.Equal(t, map[string]string{"ReadFile": "read_file"}, rev)
	})

	t.Run("pin follows the plan, not an independent fold", func(t *testing.T) {
		// planToolRenames skips "my_tool" because "MyTool" is already taken.
		// Folding tool_choice independently would pin "MyTool" — a name that
		// is in tools[], but the *wrong* tool.
		req := &anthropic.MessageNewParams{
			Tools:      []anthropic.ToolUnionParam{v1Tool("my_tool"), v1Tool("MyTool")},
			ToolChoice: anthropic.ToolChoiceParamOfTool("my_tool"),
		}
		rev := remapRequestToolNames(req)
		assert.Empty(t, rev)
		assert.Equal(t, "my_tool", req.ToolChoice.OfTool.Name)
	})

	t.Run("pin naming a tool absent from tools is untouched", func(t *testing.T) {
		req := &anthropic.MessageNewParams{
			Tools:      []anthropic.ToolUnionParam{v1Tool("read_file")},
			ToolChoice: anthropic.ToolChoiceParamOfTool("not_declared"),
		}
		remapRequestToolNames(req)
		assert.Equal(t, "not_declared", req.ToolChoice.OfTool.Name)
	})

	t.Run("non-tool tool_choice variants are inert", func(t *testing.T) {
		req := &anthropic.MessageNewParams{
			Tools:      []anthropic.ToolUnionParam{v1Tool("read_file")},
			ToolChoice: anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}},
		}
		require.NotPanics(t, func() { remapRequestToolNames(req) })
		assert.Nil(t, req.ToolChoice.OfTool)
		assert.NotNil(t, req.ToolChoice.OfAuto)
	})
}

func TestRemapBetaRequestToolNames_RemapsToolChoice(t *testing.T) {
	t.Run("pinned tool follows the fold", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools:      []anthropic.BetaToolUnionParam{betaTool("read_file")},
			ToolChoice: anthropic.BetaToolChoiceParamOfTool("read_file"),
		}
		rev := remapBetaRequestToolNames(req)
		assert.Equal(t, "ReadFile", req.Tools[0].OfTool.Name)
		assert.Equal(t, "ReadFile", req.ToolChoice.OfTool.Name)
		assert.Equal(t, map[string]string{"ReadFile": "read_file"}, rev)
	})

	t.Run("pin follows the plan, not an independent fold", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools:      []anthropic.BetaToolUnionParam{betaTool("my_tool"), betaTool("MyTool")},
			ToolChoice: anthropic.BetaToolChoiceParamOfTool("my_tool"),
		}
		rev := remapBetaRequestToolNames(req)
		assert.Empty(t, rev)
		assert.Equal(t, "my_tool", req.ToolChoice.OfTool.Name)
	})

	t.Run("pin naming a tool absent from tools is untouched", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools:      []anthropic.BetaToolUnionParam{betaTool("read_file")},
			ToolChoice: anthropic.BetaToolChoiceParamOfTool("not_declared"),
		}
		remapBetaRequestToolNames(req)
		assert.Equal(t, "not_declared", req.ToolChoice.OfTool.Name)
	})

	t.Run("non-tool tool_choice variants are inert", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools:      []anthropic.BetaToolUnionParam{betaTool("read_file")},
			ToolChoice: anthropic.BetaToolChoiceUnionParam{OfAuto: &anthropic.BetaToolChoiceAutoParam{}},
		}
		require.NotPanics(t, func() { remapBetaRequestToolNames(req) })
		assert.Nil(t, req.ToolChoice.OfTool)
		assert.NotNil(t, req.ToolChoice.OfAuto)
	})
}

// ===================================================================
// tool_use blocks in prior turns must agree with tools[]
// ===================================================================

func TestRemapRequestToolNames_RemapsHistoricalToolUse(t *testing.T) {
	t.Run("prior tool_use follows the fold", func(t *testing.T) {
		req := &anthropic.MessageNewParams{
			Tools: []anthropic.ToolUnionParam{v1Tool("read_file")},
			Messages: []anthropic.MessageParam{{
				Role: anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{{
					OfToolUse: &anthropic.ToolUseBlockParam{ID: "toolu_1", Name: "read_file"},
				}},
			}},
		}
		remapRequestToolNames(req)
		assert.Equal(t, "ReadFile", req.Messages[0].Content[0].OfToolUse.Name,
			"history must not keep the snake_case name the tools[] fold removed")
	})

	t.Run("history name absent from the plan is untouched", func(t *testing.T) {
		req := &anthropic.MessageNewParams{
			Tools: []anthropic.ToolUnionParam{v1Tool("read_file")},
			Messages: []anthropic.MessageParam{{
				Role: anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{{
					OfToolUse: &anthropic.ToolUseBlockParam{ID: "toolu_1", Name: "retired_tool"},
				}},
			}},
		}
		remapRequestToolNames(req)
		assert.Equal(t, "retired_tool", req.Messages[0].Content[0].OfToolUse.Name)
	})

	t.Run("non tool_use blocks are inert", func(t *testing.T) {
		req := &anthropic.MessageNewParams{
			Tools: []anthropic.ToolUnionParam{v1Tool("read_file")},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hi")),
			},
		}
		require.NotPanics(t, func() { remapRequestToolNames(req) })
		assert.Equal(t, "hi", req.Messages[0].Content[0].OfText.Text)
	})
}

func TestRemapBetaRequestToolNames_RemapsHistoricalToolUse(t *testing.T) {
	t.Run("prior tool_use follows the fold", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools: []anthropic.BetaToolUnionParam{betaTool("read_file")},
			Messages: []anthropic.BetaMessageParam{{
				Role: anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{{
					OfToolUse: &anthropic.BetaToolUseBlockParam{ID: "toolu_1", Name: "read_file"},
				}},
			}},
		}
		remapBetaRequestToolNames(req)
		assert.Equal(t, "ReadFile", req.Messages[0].Content[0].OfToolUse.Name)
	})

	t.Run("history name absent from the plan is untouched", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools: []anthropic.BetaToolUnionParam{betaTool("read_file")},
			Messages: []anthropic.BetaMessageParam{{
				Role: anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{{
					OfToolUse: &anthropic.BetaToolUseBlockParam{ID: "toolu_1", Name: "retired_tool"},
				}},
			}},
		}
		remapBetaRequestToolNames(req)
		assert.Equal(t, "retired_tool", req.Messages[0].Content[0].OfToolUse.Name)
	})

	t.Run("non tool_use blocks are inert", func(t *testing.T) {
		req := &anthropic.BetaMessageNewParams{
			Tools: []anthropic.BetaToolUnionParam{betaTool("read_file")},
			Messages: []anthropic.BetaMessageParam{
				anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("hi")),
			},
		}
		require.NotPanics(t, func() { remapBetaRequestToolNames(req) })
		assert.Equal(t, "hi", req.Messages[0].Content[0].OfText.Text)
	})
}

// TestRemapRequestToolNames_MCPToolsSurviveEndToEnd pins the whole request
// shape for a mixed Hermes-style toolset: MCP names pass through untouched,
// bare names fold, and every site agrees.
func TestRemapRequestToolNames_MixedMCPAndBareTools(t *testing.T) {
	req := &anthropic.MessageNewParams{
		Tools: []anthropic.ToolUnionParam{
			v1Tool("mcp__github__get_pull_request"),
			v1Tool("tingly_box_mcp__webtools__mcp_web_search"),
			v1Tool("read_file"),
			v1Tool("ls"),
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool("mcp__github__get_pull_request"),
		Messages: []anthropic.MessageParam{{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				{OfToolUse: &anthropic.ToolUseBlockParam{ID: "t1", Name: "read_file"}},
				{OfToolUse: &anthropic.ToolUseBlockParam{ID: "t2", Name: "mcp__github__get_pull_request"}},
			},
		}},
	}
	rev := remapRequestToolNames(req)

	assert.Equal(t, "mcp__github__get_pull_request", req.Tools[0].OfTool.Name)
	assert.Equal(t, "tingly_box_mcp__webtools__mcp_web_search", req.Tools[1].OfTool.Name)
	assert.Equal(t, "ReadFile", req.Tools[2].OfTool.Name)
	assert.Equal(t, "LS", req.Tools[3].OfTool.Name)

	assert.Equal(t, "mcp__github__get_pull_request", req.ToolChoice.OfTool.Name)
	assert.Equal(t, "ReadFile", req.Messages[0].Content[0].OfToolUse.Name)
	assert.Equal(t, "mcp__github__get_pull_request", req.Messages[0].Content[1].OfToolUse.Name)

	// Only the folded names need restoring on the way back.
	assert.Equal(t, map[string]string{"ReadFile": "read_file", "LS": "ls"}, rev)
}

// sseLine builds one SSE event frame.
func sseLine(event string, payload any) string {
	b, _ := json.Marshal(payload)
	return "event: " + event + "\ndata: " + string(b) + "\n\n"
}
func TestSSEToolNameRewriter(t *testing.T) {
	reverse := map[string]string{"ReadFile": "read_file"}

	t.Run("rewrites tool_use name in content_block_start", func(t *testing.T) {
		body := sseLine("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    "toolu_1",
				"name":  "ReadFile",
				"input": map[string]any{},
			},
		})
		out := readAll(t, body, reverse)
		assert.Contains(t, out, `"name":"read_file"`)
		assert.NotContains(t, out, "ReadFile")
		// Framing preserved.
		assert.True(t, strings.HasPrefix(out, "event: content_block_start\ndata: "))
		assert.True(t, strings.HasSuffix(out, "\n\n"))
	})

	t.Run("passes through unrelated events byte-for-byte", func(t *testing.T) {
		body := sseLine("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"path":`},
		}) + sseLine("message_stop", map[string]any{"type": "message_stop"})
		assert.Equal(t, body, readAll(t, body, reverse))
	})

	t.Run("leaves names absent from the reverse map alone", func(t *testing.T) {
		body := sseLine("content_block_start", map[string]any{
			"type": "content_block_start",
			"content_block": map[string]any{
				"type": "tool_use", "id": "toolu_2", "name": "SomethingElse",
			},
		})
		assert.Equal(t, body, readAll(t, body, reverse))
	})

	t.Run("text content_block_start untouched", func(t *testing.T) {
		body := sseLine("content_block_start", map[string]any{
			"type":          "content_block_start",
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		assert.Equal(t, body, readAll(t, body, reverse))
	})

	t.Run("handles a multi-event stream and tiny reads", func(t *testing.T) {
		body := sseLine("message_start", map[string]any{"type": "message_start"}) +
			sseLine("content_block_start", map[string]any{
				"type": "content_block_start",
				"content_block": map[string]any{
					"type": "tool_use", "id": "toolu_3", "name": "ReadFile",
				},
			}) +
			sseLine("message_stop", map[string]any{"type": "message_stop"})

		r := newSSEToolNameRewriter(io.NopCloser(strings.NewReader(body)), reverse)
		var sb strings.Builder
		buf := make([]byte, 3) // force many partial reads
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
		}
		out := sb.String()
		assert.Contains(t, out, `"name":"read_file"`)
		assert.Contains(t, out, "message_start")
		assert.Contains(t, out, "message_stop")
	})

	t.Run("malformed json passes through", func(t *testing.T) {
		body := "data: {\"type\":\"content_block_start\",\"tool_use\" BROKEN\n\n"
		assert.Equal(t, body, readAll(t, body, reverse))
	})

	t.Run("empty reverse map is inert", func(t *testing.T) {
		body := sseLine("content_block_start", map[string]any{
			"type": "content_block_start",
			"content_block": map[string]any{
				"type": "tool_use", "id": "toolu_4", "name": "ReadFile",
			},
		})
		assert.Equal(t, body, readAll(t, body, map[string]string{}))
	})
}

func readAll(t *testing.T, body string, reverse map[string]string) string {
	t.Helper()
	r := newSSEToolNameRewriter(io.NopCloser(strings.NewReader(body)), reverse)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(out)
}
