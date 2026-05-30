package cmdwrap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestParseAllCommandsPreservesStructuredArgs(t *testing.T) {
	commands := config.RunCommands{
		{
			Command: "tool",
			Args: configutil.StringArray{
				"hello world",
				`C:\Program Files\App\app.exe`,
				"${NAME}",
			},
		},
	}

	cmds := ParseAllCommands(commands, "task", zap.NewNop(), map[string]string{"NAME": "value with spaces"}, "workdir", idgen.NewGenIDInterpolator())
	require.Len(t, cmds, 1)

	assert.Equal(t, "tool", cmds[0].executable)
	assert.Equal(t, []string{"hello world", `C:\Program Files\App\app.exe`, "value with spaces"}, cmds[0].args)
	assert.Equal(t, "workdir", cmds[0].cwd)
}

func TestParseAllCommandsParsesLegacyStringsWithQuotes(t *testing.T) {
	commands := config.RunCommands{
		{
			Command: `tool "hello world" C:\Tools\app.exe "C:\Program Files\App\app.exe"`,
			Legacy:  true,
		},
	}

	cmds := ParseAllCommands(commands, "task", zap.NewNop(), nil, "", idgen.NewGenIDInterpolator())
	require.Len(t, cmds, 1)

	assert.Equal(t, "tool", cmds[0].executable)
	assert.Equal(t, []string{"hello world", `C:\Tools\app.exe`, `C:\Program Files\App\app.exe`}, cmds[0].args)
}

func TestParseAllCommandsUsesPlatformShellForShellCommands(t *testing.T) {
	commands := config.RunCommands{
		{
			Command: `echo "${MESSAGE}"`,
			Shell:   true,
		},
	}

	cmds := ParseAllCommands(commands, "task", zap.NewNop(), map[string]string{"MESSAGE": "hello"}, "", idgen.NewGenIDInterpolator())
	require.Len(t, cmds, 1)

	if runtime.GOOS == "windows" {
		assert.Equal(t, "cmd.exe", cmds[0].executable)
		assert.Equal(t, []string{"/C", `echo "hello"`}, cmds[0].args)
	} else {
		assert.Equal(t, "sh", cmds[0].executable)
		assert.Equal(t, []string{"-c", `echo "hello"`}, cmds[0].args)
	}
}

func TestCommandWrapperRunAppliesCwdAndEnv(t *testing.T) {
	if os.Getenv("ASYNK_CMDWRAP_HELPER") == "1" {
		return
	}

	dir := t.TempDir()
	outputPath := filepath.Join(dir, "helper-output.txt")
	executable, err := os.Executable()
	require.NoError(t, err)

	wrapper := &CommandWrapper{
		executable: executable,
		args:       []string{"-test.run=TestCommandWrapperHelperProcess", "--", outputPath},
		cwd:        dir,
		taskId:     "task",
		log:        zap.NewNop(),
	}

	env := append(os.Environ(), "ASYNK_CMDWRAP_HELPER=1", "ASYNK_EXPECTED=value")
	err = wrapper.Run(context.Background(), env, NewWrapLogger([]string{"task"}))
	require.NoError(t, err)

	output, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	parts := strings.Split(string(output), "\n")
	require.Len(t, parts, 2)
	assert.Equal(t, "value", parts[0])
	assert.Equal(t, filepath.Clean(dir), filepath.Clean(parts[1]))
}

func TestCommandWrapperHelperProcess(t *testing.T) {
	if os.Getenv("ASYNK_CMDWRAP_HELPER") != "1" {
		return
	}

	outputPath := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			outputPath = os.Args[i+1]
			break
		}
	}

	if outputPath == "" {
		os.Exit(2)
	}

	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}

	err = os.WriteFile(outputPath, []byte(os.Getenv("ASYNK_EXPECTED")+"\n"+cwd), 0644)
	if err != nil {
		os.Exit(2)
	}

	os.Exit(0)
}
