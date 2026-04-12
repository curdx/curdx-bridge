// curdx-installer-helper replaces the inline Python calls in install.sh
// with a single, dependency-free Go binary.
//
// Usage:
//
//	curdx-installer-helper <subcommand> [args...]
//
// Subcommands:
//
//	check-mcp-has-codex   <json-file>
//	remove-codex-mcp      <json-file>
//	replace-block         <file> <template-file> <start-marker> <end-marker>
//	remove-legacy-md-rules <file>
//	settings-add-permission    <settings-file> <permission>
//	settings-remove-permission <settings-file> <permission>
//	remove-tmux-block     <file>
//	file-replace          <file> <placeholder> <replacement>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(0)
	}

	var err error
	switch os.Args[1] {
	case "check-mcp-has-codex":
		err = cmdCheckMCPHasCodex(os.Args[2:])
	case "remove-codex-mcp":
		err = cmdRemoveCodexMCP(os.Args[2:])
	case "replace-block":
		err = cmdReplaceBlock(os.Args[2:])
	case "remove-legacy-md-rules":
		err = cmdRemoveLegacyMDRules(os.Args[2:])
	case "settings-add-permission":
		err = cmdSettingsAddPermission(os.Args[2:])
	case "settings-remove-permission":
		err = cmdSettingsRemovePermission(os.Args[2:])
	case "remove-tmux-block":
		err = cmdRemoveTmuxBlock(os.Args[2:])
	case "file-replace":
		err = cmdFileReplace(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "WARN: unknown subcommand: %s\n", os.Args[1])
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: %s: %v\n", os.Args[1], err)
	}
	// Always exit 0 to avoid blocking the install flow.
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: curdx-installer-helper <subcommand> [args...]

Subcommands:
  check-mcp-has-codex   <json-file>
  remove-codex-mcp      <json-file>
  replace-block         <file> <template-file> <start-marker> <end-marker>
  remove-legacy-md-rules <file>
  settings-add-permission    <settings-file> <permission>
  settings-remove-permission <settings-file> <permission>
  remove-tmux-block     <file>
  file-replace          <file> <placeholder> <replacement>`)
}

// ---------------------------------------------------------------------------
// 1. check-mcp-has-codex <json-file>
// ---------------------------------------------------------------------------

func cmdCheckMCPHasCodex(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: check-mcp-has-codex <json-file>")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Println("no")
		return err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		fmt.Println("no")
		return nil
	}

	projectsRaw, ok := root["projects"]
	if !ok {
		fmt.Println("no")
		return nil
	}

	var projects map[string]json.RawMessage
	if err := json.Unmarshal(projectsRaw, &projects); err != nil {
		fmt.Println("no")
		return nil
	}

	for _, projRaw := range projects {
		var projCfg map[string]json.RawMessage
		if err := json.Unmarshal(projRaw, &projCfg); err != nil {
			continue
		}
		serversRaw, ok := projCfg["mcpServers"]
		if !ok {
			continue
		}
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(serversRaw, &servers); err != nil {
			continue
		}
		for name := range servers {
			if strings.Contains(strings.ToLower(name), "codex") {
				fmt.Println("yes")
				return nil
			}
		}
	}

	fmt.Println("no")
	return nil
}

// ---------------------------------------------------------------------------
// 2. remove-codex-mcp <json-file>
// ---------------------------------------------------------------------------

func cmdRemoveCodexMCP(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remove-codex-mcp <json-file>")
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Decode into ordered-preserving structure using interface{}.
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}

	projects, ok := root["projects"].(map[string]interface{})
	if !ok {
		return nil // nothing to do
	}

	var removed []string
	for proj, cfgRaw := range projects {
		cfg, ok := cfgRaw.(map[string]interface{})
		if !ok {
			continue
		}
		servers, ok := cfg["mcpServers"].(map[string]interface{})
		if !ok {
			continue
		}
		for name := range servers {
			if strings.Contains(strings.ToLower(name), "codex") {
				removed = append(removed, proj+": "+name)
				delete(servers, name)
			}
		}
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0644); err != nil {
		return err
	}

	if len(removed) > 0 {
		fmt.Println("Removed the following MCP configurations:")
		for _, r := range removed {
			fmt.Printf("  - %s\n", r)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 3. replace-block <file> <template-file> <start-marker> <end-marker>
// ---------------------------------------------------------------------------

func cmdReplaceBlock(args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("usage: replace-block <file> <template-file> <start-marker> <end-marker>")
	}
	filePath, templatePath, startMarker, endMarker := args[0], args[1], args[2], args[3]

	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	tmpl, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}

	text := string(content)
	newBlock := strings.TrimSpace(string(tmpl))

	// Count how many start-marker/end-marker pairs exist.
	count := 0
	tmp := text
	for {
		si := strings.Index(tmp, startMarker)
		if si < 0 {
			break
		}
		ei := strings.Index(tmp[si:], endMarker)
		if ei < 0 {
			break
		}
		count++
		tmp = tmp[si+ei+len(endMarker):]
	}

	if count == 0 {
		// No markers found; nothing to replace.
		return nil
	}

	if count == 1 {
		// Exactly one pair: replace in place.
		si := strings.Index(text, startMarker)
		ei := strings.Index(text[si:], endMarker)
		text = text[:si] + newBlock + text[si+ei+len(endMarker):]
	} else {
		// Multiple pairs: remove all, then append template at end.
		for {
			si := strings.Index(text, startMarker)
			if si < 0 {
				break
			}
			ei := strings.Index(text[si:], endMarker)
			if ei < 0 {
				break
			}
			text = text[:si] + text[si+ei+len(endMarker):]
		}
		text = strings.TrimRight(text, " \t\r\n") + "\n\n" + newBlock + "\n"
	}

	return os.WriteFile(filePath, []byte(text), 0644)
}

// ---------------------------------------------------------------------------
// 4. remove-legacy-md-rules <file>
// ---------------------------------------------------------------------------

func cmdRemoveLegacyMDRules(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remove-legacy-md-rules <file>")
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	// We need to remove sections that start with specific ## headings and extend
	// to the next ## heading (or end of file). For the "Codex Collaboration Rules"
	// heading specifically, we must NOT stop at "## Gemini" (treat Gemini sections
	// as part of the Codex section -- they will be removed by their own pattern).
	//
	// Go's RE2 does not support lookahead, so we use a function-based approach:
	// find each heading, then scan forward to find where the section ends.

	headings := []string{
		"## Codex Collaboration Rules",
		"## Codex 协作规则",
		"## Gemini Collaboration Rules",
		"## Gemini 协作规则",
		"## OpenCode Collaboration Rules",
		"## OpenCode 协作规则",
	}

	// Build a set of heading prefixes to treat as "continue" for Codex rules.
	geminiPrefixes := []string{
		"## Gemini Collaboration Rules",
		"## Gemini 协作规则",
	}

	for _, heading := range headings {
		for {
			idx := strings.Index(content, heading)
			if idx < 0 {
				break
			}
			// Find end of this section: next "## " that is NOT a Gemini heading
			// (for Codex rules), or end of file.
			isCodexRule := strings.HasPrefix(heading, "## Codex")
			end := len(content)
			pos := idx + len(heading)
			for pos < len(content) {
				nlIdx := strings.Index(content[pos:], "\n## ")
				if nlIdx < 0 {
					break
				}
				nextHeadingStart := pos + nlIdx + 1 // position of "## "
				// Check if this next heading is a Gemini heading (skip it for Codex rules).
				if isCodexRule {
					isGemini := false
					for _, gp := range geminiPrefixes {
						if strings.HasPrefix(content[nextHeadingStart:], gp) {
							isGemini = true
							break
						}
					}
					if isGemini {
						pos = nextHeadingStart + 1
						continue
					}
				}
				end = nextHeadingStart
				break
			}
			content = content[:idx] + content[end:]
		}
	}

	content = strings.TrimRight(content, " \t\r\n") + "\n"
	return os.WriteFile(path, []byte(content), 0644)
}

// ---------------------------------------------------------------------------
// 5. settings-add-permission <settings-file> <permission>
// ---------------------------------------------------------------------------

func cmdSettingsAddPermission(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: settings-add-permission <settings-file> <permission>")
	}
	path, perm := args[0], args[1]

	root, err := readJSONObject(path)
	if err != nil {
		return err
	}

	allow := ensurePermissionsAllow(root)
	for _, v := range allow {
		if s, ok := v.(string); ok && s == perm {
			// Already present.
			return writeJSONFile(path, root)
		}
	}
	allow = append(allow, perm)
	root["permissions"].(map[string]interface{})["allow"] = allow

	return writeJSONFile(path, root)
}

// ---------------------------------------------------------------------------
// 6. settings-remove-permission <settings-file> <permission>
// ---------------------------------------------------------------------------

func cmdSettingsRemovePermission(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: settings-remove-permission <settings-file> <permission>")
	}
	path, perm := args[0], args[1]

	root, err := readJSONObject(path)
	if err != nil {
		return err
	}

	allow := ensurePermissionsAllow(root)
	filtered := make([]interface{}, 0, len(allow))
	for _, v := range allow {
		if s, ok := v.(string); ok && s == perm {
			continue
		}
		filtered = append(filtered, v)
	}
	root["permissions"].(map[string]interface{})["allow"] = filtered

	return writeJSONFile(path, root)
}

// ---------------------------------------------------------------------------
// 7. remove-tmux-block <file>
// ---------------------------------------------------------------------------

func cmdRemoveTmuxBlock(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remove-tmux-block <file>")
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	// New format: block surrounded by "# ===..." lines with known header/footer.
	//   # ====...
	//   # CURDX (CURDX Bridge) tmux configuration
	//   ...
	//   # End of CURDX tmux configuration
	//   # ====...
	reNew := regexp.MustCompile(`(?s)\n*# =+\n# CURDX \((?:Claude Code|CURDX) Bridge\) tmux configuration.*?# End of CURDX tmux configuration\n# =+`)
	content = reNew.ReplaceAllString(content, "")

	// Legacy format: everything from "# CURDX tmux configuration" to EOF.
	reLegacy := regexp.MustCompile(`(?s)\n*# CURDX tmux configuration.*`)
	content = reLegacy.ReplaceAllString(content, "")

	content = strings.TrimSpace(content)
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// ---------------------------------------------------------------------------
// 8. file-replace <file> <placeholder> <replacement>
// ---------------------------------------------------------------------------

func cmdFileReplace(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: file-replace <file> <placeholder> <replacement>")
	}
	filePath, placeholder, replacement := args[0], args[1], args[2]

	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	result := strings.ReplaceAll(string(data), placeholder, replacement)
	_, err = fmt.Fprint(os.Stdout, result)
	return err
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// readJSONObject reads a JSON file and returns the top-level object.
// If the file does not exist, returns an empty map.
func readJSONObject(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, err
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		root = make(map[string]interface{})
	}
	return root, nil
}

// ensurePermissionsAllow makes sure root["permissions"]["allow"] exists as a
// []interface{} and returns it.
func ensurePermissionsAllow(root map[string]interface{}) []interface{} {
	permsRaw, ok := root["permissions"]
	if !ok {
		perms := map[string]interface{}{
			"allow": []interface{}{},
			"deny":  []interface{}{},
		}
		root["permissions"] = perms
		return perms["allow"].([]interface{})
	}
	perms, ok := permsRaw.(map[string]interface{})
	if !ok {
		perms = map[string]interface{}{
			"allow": []interface{}{},
			"deny":  []interface{}{},
		}
		root["permissions"] = perms
		return perms["allow"].([]interface{})
	}
	allowRaw, ok := perms["allow"]
	if !ok {
		perms["allow"] = []interface{}{}
		return perms["allow"].([]interface{})
	}
	allow, ok := allowRaw.([]interface{})
	if !ok {
		perms["allow"] = []interface{}{}
		return perms["allow"].([]interface{})
	}
	return allow
}

// writeJSONFile marshals data with indent 2 and writes it to path.
func writeJSONFile(path string, data interface{}) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
}
