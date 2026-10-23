package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	"github.com/spf13/cobra"
)

// newTapeCommand creates the parent command for managing and running tape scripts
// and registers its playback, validation, listing, directory, deletion, and show subcommands.
func newTapeCommand() *cobra.Command {
	tapeCmd := &cobra.Command{
		Use:   "tape",
		Short: "Manage and run .tape automation scripts",
		Long: `Manage and execute .tape automation scripts for TUIOS

Tape files allow you to automate interactions with TUIOS by specifying
sequences of commands, key presses, and delays. Execute scripts in
interactive mode (visible TUI) to watch automation happen in real-time.`,
		Example: `  # Run tape with visible TUI (watch it happen)
  tuios tape play demo.tape

  # Validate tape file syntax
  tuios tape validate demo.tape`,
	}

	// register tape sub commands
	tapeCmd.AddCommand(
		newTapePlayCommand(),
		newTapeValidateCommand(),
		newTapeListCommand(),
		newTapeDirCommand(),
		newTapeDelCommand(),
		newTapeShowCommand(),
	)

	return tapeCmd
}

// newTapePlayCommand creates the command that runs a tape file with the TUI visible.
func newTapePlayCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "play <file.tape>",
		Short: "Run a tape file in interactive mode",
		Long: `Execute a tape script while displaying the TUIOS TUI

In interactive mode, you can see the automation happening in real-time
in the terminal UI. Press Ctrl+P to pause/resume playback.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runTapeInteractive(args[0])
		},
	}
}

// newTapeValidateCommand creates the command that checks a tape file and summarizes
// its parsed commands without executing them.
func newTapeValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file.tape>",
		Short: "Validate a tape file without running it",
		Long:  `Check if a tape file is syntactically correct`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return validateTapeFile(args[0])
		},
	}
}

// newTapeListCommand creates the command that lists saved tape recordings, either
// as human-readable output or JSON when the --json flag is set.
func newTapeListCommand() *cobra.Command {
	var tapeListJSON bool
	tapeListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all saved tape recordings",
		Long: `List the tape files in the tape recordings directory, with the size and the
time each one last changed. 'tuios tape dir' prints the directory.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return listTapeFiles(tapeListJSON)
		},
	}
	tapeListCmd.Flags().BoolVar(&tapeListJSON, "json", false, "Output as JSON")
	return tapeListCmd
}

// newTapeDirCommand creates the command that prints the directory used to store
// tape recordings.
func newTapeDirCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dir",
		Short: "Show the tape recordings directory path",
		Long:  `Print the path where tape recordings are stored`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return showTapeDirectory()
		},
	}
}

// newTapeDelCommand creates the command that prompts for confirmation before
// deleting a named tape recording.
func newTapeDelCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a tape recording",
		Long:  `Delete a tape file from the recordings directory`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return deleteTapeFile(args[0])
		},
	}
}

// newTapeShowCommand creates the command that prints a saved tape recording's
// contents and file path.
func newTapeShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Display the contents of a tape file",
		Long:  `Print the contents of a tape recording to stdout`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return showTapeFile(args[0])
		},
	}
}

// runTapeInteractive loads and parses a tape file, configures TUIOS for script
// playback, runs the interactive program, and returns any playback or program error.
func runTapeInteractive(tapeFile string) error {
	script, err := tape.LoadFile(tapeFile)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}
	if len(script.Errors) > 0 {
		fmt.Fprintf(os.Stderr, "Tape parsing errors:\n")
		for _, err := range script.Errors {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", tapeFile, err)
		}
		return fmt.Errorf("the tape file has errors, listed above. Fix them, then run it again")
	}
	commands := script.Commands

	fmt.Printf("Preparing tape script: %s\n", tapeFile)
	fmt.Printf("Total commands: %d\n", len(commands))
	fmt.Println("Press Ctrl+C to cancel, Ctrl+P to pause/resume playback")
	fmt.Println("\nStarting TUIOS with tape playback...")

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	// LoadUserConfig no longer applies globals; apply the config appearance so
	// tape playback honors the user's borders/dock/etc. Animations are forced
	// off below for deterministic playback.
	config.ApplyAppearanceConfig(userConfig, &config.Global)

	if err := theme.Initialize(interfaceFlags.ThemeName); err != nil {
		log.Printf("Warning: Failed to load theme '%s': %v", interfaceFlags.ThemeName, err)
	}

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Force animations off for deterministic playback of hand-written tapes,
	// matching recorded tapes (the recorder prepends DisableAnimations). Tapes
	// can still re-enable them explicitly with EnableAnimations.
	config.Global.Motion = config.MotionNone

	player := tape.NewPlayer(commands)

	// The factory, the same as every other client, so the tape's model has
	// what a session has: capabilities, passthroughs, hooks, channels. It used
	// to be a struct literal that named a dozen fields by hand and had none of
	// the rest.
	initialOS := app.NewOS(app.OSOptions{
		Client:          app.ClientLocal,
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		ShowKeys:        interfaceFlags.ShowKeys,
	})
	initialOS.ScriptMode = true
	initialOS.ScriptPlayer = player
	initialOS.ScriptPaused = false
	initialOS.ScriptExecutor = tape.NewCommandExecutor(initialOS)

	p := tea.NewProgram(initialOS, app.ProgramOptions()...)
	initialOS.BindProgram(p)

	// A quit the event loop cannot carry out still has to end the process;
	// finish runs once the cleanup below is done. See armSignalQuit.
	runReturned, finish := armSignalQuit(p)

	finalModel, err := p.Run()
	runReturned()

	failure := ""
	if finalOS, ok := finalModel.(*app.OS); ok {
		failure = finalOS.ScriptFailure
		finalOS.Cleanup()
		_, _ = os.Stdout.WriteString(finalOS.HostProgramStatusClear())
	}

	terminal.ResetTerminal()
	finish()

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}
	if failure != "" {
		return fmt.Errorf("the tape stopped at %s: %s", tapeFile, failure)
	}

	return nil
}

// validateTapeFile parses a tape file without executing it, reports parse errors,
// and prints a summary of the valid commands.
func validateTapeFile(tapeFile string) error {
	script, err := tape.LoadFile(tapeFile)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}

	commands, parseErrors := script.Commands, script.Errors
	if len(parseErrors) > 0 {
		fmt.Fprintf(os.Stderr, "Parsing errors found:\n")
		// file:line, column: the shape an editor's error list can jump to.
		for _, err := range parseErrors {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %s\n", tapeFile, err)
		}
		return fmt.Errorf("the tape file has errors, listed above. Fix them, then validate it again")
	}

	checkmark := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("✓")
	validText := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("Tape file is valid")

	fmt.Printf("%s %s\n", checkmark, validText)

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

	fmt.Printf("  %s: %s\n", labelStyle.Render("File"), valueStyle.Render(tapeFile))
	fmt.Printf("  %s: %s\n", labelStyle.Render("Commands"), valueStyle.Render(fmt.Sprintf("%d", len(commands))))

	if len(commands) > 0 {
		fmt.Print("\n  ")
		fmt.Print(headerStyle.Render("Command Summary:"))
		fmt.Println()

		numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		cmdNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
		argsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

		for i, cmd := range commands {
			parts := strings.Split(cmd.String(), " ")
			if len(parts) > 1 {
				cmdName := parts[0]
				args := strings.Join(parts[1:], " ")
				fmt.Printf("    %s %s %s\n",
					numberStyle.Render(fmt.Sprintf("[%d]", i+1)),
					cmdNameStyle.Render(cmdName),
					argsStyle.Render(args))
			} else {
				fmt.Printf("    %s %s\n",
					numberStyle.Render(fmt.Sprintf("[%d]", i+1)),
					cmdNameStyle.Render(parts[0]))
			}
		}
	}

	return nil
}

// listTapeFiles loads saved recordings and prints their names, sizes, and modified
// times, or returns the same information as JSON when asJSON is true.
func listTapeFiles(asJSON bool) error {
	files, err := app.LoadTapeFiles()
	if err != nil {
		return fmt.Errorf("failed to load tape files: %w", err)
	}

	tapeDir, _ := app.GetTapeDirectory()
	if asJSON {
		type tapeRow struct {
			Name     string    `json:"name"`
			Path     string    `json:"path"`
			Size     int64     `json:"size"`
			Modified time.Time `json:"modified"`
		}
		rows := make([]tapeRow, 0, len(files))
		for _, f := range files {
			rows = append(rows, tapeRow{Name: f.Name, Path: f.Path, Size: f.Size, Modified: f.Modified})
		}
		return printJSON(map[string]any{"dir": tapeDir, "tapes": rows})
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	fmt.Printf("%s\n", headerStyle.Render("Tape Recordings"))
	fmt.Printf("%s\n\n", pathStyle.Render("Location: "+tapeDir))

	if len(files) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		fmt.Printf("%s\n", dimStyle.Render("No tape recordings found"))
		fmt.Printf("%s\n", dimStyle.Render("Use Ctrl+B, T, r in TUIOS to start recording"))
		return nil
	}

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	sizeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	for _, file := range files {
		sizeStr := units.ShortBytes(file.Size)
		dateStr := file.Modified.Format("2006-01-02 15:04")
		fmt.Printf("  %s  %s  %s\n",
			nameStyle.Render(fmt.Sprintf("%-30s", file.Name)),
			sizeStyle.Render(fmt.Sprintf("%8s", sizeStr)),
			dateStyle.Render(dateStr))
	}

	fmt.Printf("\n%s found\n", plural.Count(len(files), "tape"))
	return nil
}

// showTapeDirectory prints the path where tape recordings are stored.
func showTapeDirectory() error {
	tapeDir, err := app.GetTapeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get tape directory: %w", err)
	}
	fmt.Println(tapeDir)
	return nil
}

// deleteTapeFile finds a recording by name, asks for confirmation, and deletes it
// if the user approves.
func deleteTapeFile(name string) error {
	files, err := app.LoadTapeFiles()
	if err != nil {
		return fmt.Errorf("failed to load tape files: %w", err)
	}

	var targetFile *app.TapeFile
	for i := range files {
		if files[i].Name == name || files[i].Name == strings.TrimSuffix(name, ".tape") {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return fmt.Errorf("no tape file named %q. Run 'tuios tape list' to see the tape files", name)
	}

	fmt.Printf("Delete '%s'? (yes/no): ", targetFile.Name)
	var response string
	_, _ = fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "yes" && response != "y" {
		fmt.Println("Deletion cancelled.")
		return nil
	}

	if err := app.DeleteTapeFile(targetFile.Path); err != nil {
		return fmt.Errorf("failed to delete tape file: %w", err)
	}

	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	fmt.Printf("%s\n", successStyle.Render("Deleted: "+targetFile.Name))
	return nil
}

// showTapeFile finds a saved recording by name and prints its path and contents.
func showTapeFile(name string) error {
	files, err := app.LoadTapeFiles()
	if err != nil {
		return fmt.Errorf("failed to load tape files: %w", err)
	}

	var targetFile *app.TapeFile
	for i := range files {
		if files[i].Name == name || files[i].Name == strings.TrimSuffix(name, ".tape") {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return fmt.Errorf("no tape file named %q. Run 'tuios tape list' to see the tape files", name)
	}

	content, err := os.ReadFile(targetFile.Path)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	fmt.Printf("%s\n", headerStyle.Render(targetFile.Name+".tape"))
	fmt.Printf("%s\n\n", pathStyle.Render(targetFile.Path))

	fmt.Print(string(content))

	return nil
}
