// Package main provides a demo program to test the Agent Skills functionality.
// This simulates the agent workflow without requiring a full server setup.
//
// Usage:
//
//	go run cmd/skills-demo/main.go [sandbox-type]
//
// sandbox-type can be: local, docker (default: local)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

func main() {
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println("  Agent Skills Demo - Progressive Disclosure in Action")
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println()

	ctx := context.Background()

	// Parse sandbox type from command line
	sandboxType := "local"
	if len(os.Args) > 1 {
		sandboxType = os.Args[1]
	}

	// Get the path to examples/skills
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Println("Error: Failed to get current file path")
		os.Exit(1)
	}
	skillsDir := filepath.Join(filepath.Dir(filename), "..", "..", "examples", "skills")

	fmt.Printf("📁 Skills directory: %s\n\n", skillsDir)

	// ========================================
	// Step 1: Initialize Sandbox Manager
	// ========================================
	fmt.Println("Step 1: Initialize Sandbox Manager")
	fmt.Println("-" + strings.Repeat("-", 50))
	fmt.Printf("   Requested sandbox type: %s\n", sandboxType)

	sandboxMgr, err := sandbox.NewManagerFromType(sandboxType, false) // Disable fallback to test specific mode
	if err != nil {
		fmt.Printf("Error creating sandbox: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Sandbox initialized (type: %s)\n\n", sandboxMgr.GetType())

	// ========================================
	// Step 2: Initialize Skills Manager
	// ========================================
	fmt.Println("Step 2: Initialize Skills Manager")
	fmt.Println("-" + strings.Repeat("-", 50))

	skillsConfig := &skills.ManagerConfig{
		SkillDirs:     []string{skillsDir},
		AllowedSkills: []string{}, // Allow all
		Enabled:       true,
	}

	skillsManager := skills.NewManager(skillsConfig, sandboxMgr)
	if err := skillsManager.Initialize(ctx); err != nil {
		fmt.Printf("Error initializing skills: %v\n", err)
		os.Exit(1)
	}

	metadata := skillsManager.GetAllMetadata()
	fmt.Printf("✅ Discovered %d skills\n\n", len(metadata))

	// ========================================
	// Step 3: Show Level 1 - Metadata (System Prompt)
	// ========================================
	fmt.Println("Step 3: Level 1 - Skill Metadata (injected into System Prompt)")
	fmt.Println("-" + strings.Repeat("-", 50))

	// Simulate what gets injected into system prompt
	fmt.Println("\n### Available Skills\n")
	fmt.Println("The following skills are available. When a user request matches a skill's description,")
	fmt.Println("use the `read_skill` tool to load its full instructions before proceeding.\n")
	for i, m := range metadata {
		fmt.Printf("%d. **%s**: %s\n", i+1, m.Name, m.Description)
	}
	fmt.Println("\nUse `read_skill` with the skill name to load detailed instructions when needed.")
	fmt.Println("Use `execute_skill_script` to run utility scripts bundled with a skill.")
	fmt.Println()

	// ========================================
	// Step 4: Simulate Agent Tool Calls
	// ========================================
	fmt.Println("Step 4: Simulate Agent Tool Calls")
	fmt.Println("-" + strings.Repeat("-", 50))

	// Scenario: User asks "Help me extract text from a PDF"
	fmt.Println("\n🤖 Scenario: User asks 'Help me extract text from a PDF'")
	fmt.Println("   Agent recognizes this matches 'pdf-processing' skill")
	fmt.Println()

	// ========================================
	// Step 5: Level 2 - Load Skill Instructions
	// ========================================
	fmt.Println("Step 5: Level 2 - Agent calls read_skill tool")
	fmt.Println("-" + strings.Repeat("-", 50))

	// Simulate tool call: read_skill(skill_name="pdf-processing")
	skill, err := skillsManager.LoadSkill(ctx, "pdf-processing")
	if err != nil {
		fmt.Printf("Error loading skill: %v\n", err)
	} else {
		fmt.Printf("✅ Tool: read_skill(skill_name=\"pdf-processing\")\n")
		fmt.Printf("   Success: true\n")
		fmt.Printf("   Skill: %s\n", skill.Name)
		fmt.Printf("   Description: %s\n", skill.Description)
		fmt.Printf("   Instructions preview (first 500 chars):\n")
		instructions := skill.Instructions
		if len(instructions) > 500 {
			instructions = instructions[:500] + "\n... (truncated)"
		}
		for _, line := range strings.Split(instructions, "\n") {
			fmt.Printf("   │ %s\n", line)
		}
	}
	fmt.Println()

	// ========================================
	// Step 6: Level 3 - Load Additional Resource
	// ========================================
	fmt.Println("Step 6: Level 3 - Agent reads additional file (FORMS.md)")
	fmt.Println("-" + strings.Repeat("-", 50))

	// Simulate tool call: read_skill(skill_name="pdf-processing", file_path="FORMS.md")
	formsContent, err := skillsManager.ReadSkillFile(ctx, "pdf-processing", "FORMS.md")
	if err != nil {
		fmt.Printf("Error reading FORMS.md: %v\n", err)
	} else {
		fmt.Printf("✅ Tool: read_skill(skill_name=\"pdf-processing\", file_path=\"FORMS.md\")\n")
		fmt.Printf("   Success: true\n")
		fmt.Printf("   Content length: %d characters\n", len(formsContent))
		// Show first few lines
		lines := strings.Split(formsContent, "\n")
		fmt.Printf("   Preview (first 10 lines):\n")
		for i, line := range lines {
			if i >= 10 {
				fmt.Printf("   │ ... (truncated)\n")
				break
			}
			fmt.Printf("   │ %s\n", line)
		}
	}
	fmt.Println()

	// ========================================
	// Step 7: Execute Skill Script
	// ========================================
	fmt.Println("Step 7: Agent executes skill script in sandbox")
	fmt.Println("-" + strings.Repeat("-", 50))

	// Simulate tool call: execute_skill_script(skill_name="pdf-processing", script_path="scripts/analyze_form.py", args=["test.pdf"])
	args := []string{"test.pdf"}
	argsJSON, _ := json.Marshal(args)
	fmt.Printf("✅ Tool: execute_skill_script\n")
	fmt.Printf("   skill_name: \"pdf-processing\"\n")
	fmt.Printf("   script_path: \"scripts/analyze_form.py\"\n")
	fmt.Printf("   args: %s\n", string(argsJSON))

	result, err := skillsManager.ExecuteScript(ctx, "pdf-processing", "scripts/analyze_form.py", args)
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
	} else {
		fmt.Printf("   Exit Code: %d\n", result.ExitCode)
		fmt.Printf("   Duration: %v\n", result.Duration)
		fmt.Printf("   Output:\n")
		for _, line := range strings.Split(result.Stdout, "\n") {
			fmt.Printf("   │ %s\n", line)
		}
		if result.Stderr != "" {
			fmt.Printf("   Stderr:\n")
			for _, line := range strings.Split(result.Stderr, "\n") {
				fmt.Printf("   │ %s\n", line)
			}
		}
	}
	fmt.Println()

	// ========================================
	// Step 8: List all files in skill
	// ========================================
	fmt.Println("Step 8: List all available files in skill")
	fmt.Println("-" + strings.Repeat("-", 50))

	files, err := skillsManager.ListSkillFiles(ctx, "pdf-processing")
	if err != nil {
		fmt.Printf("Error listing files: %v\n", err)
	} else {
		fmt.Printf("✅ Files in pdf-processing skill:\n")
		for _, f := range files {
			isScript := skills.IsScript(f)
			scriptTag := ""
			if isScript {
				scriptTag = " [executable]"
			}
			fmt.Printf("   - %s%s\n", f, scriptTag)
		}
	}
	fmt.Println()

	// ========================================
	// Summary
	// ========================================
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println("  Summary: Progressive Disclosure Flow")
	fmt.Println("=" + strings.Repeat("=", 70))
	fmt.Println()
	fmt.Println("  Level 1 (Metadata)     : ~100 tokens/skill in system prompt")
	fmt.Println("                          → Agent knows which skills exist")
	fmt.Println()
	fmt.Println("  Level 2 (Instructions) : Loaded on-demand via read_skill")
	fmt.Println("                          → Agent learns skill instructions")
	fmt.Println()
	fmt.Println("  Level 3 (Resources)    : Additional files loaded as needed")
	fmt.Println("                          → Agent accesses reference docs & scripts")
	fmt.Println()
	fmt.Println("  🎉 Demo completed successfully!")
	fmt.Println()
}
