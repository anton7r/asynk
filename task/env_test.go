package task

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anton7r/asynk/cmdwrap"
	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"github.com/anton7r/asynk/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMergeEnvUsesExpectedPrecedence(t *testing.T) {
	merged := MergeEnv(
		[]string{
			"SAME=parent",
			"PARENT_ONLY=parent",
		},
		map[string]string{
			"SAME":      "env-file",
			"FILE_ONLY": "file",
		},
		[]string{
			"SAME=task",
			"TASK_ONLY=${PARENT_ONLY}-${FILE_ONLY}-${SAME}",
		},
	)

	assert.Equal(t, "task", merged["SAME"])
	assert.Equal(t, "parent", merged["PARENT_ONLY"])
	assert.Equal(t, "file", merged["FILE_ONLY"])
	assert.Equal(t, "parent-file-task", merged["TASK_ONLY"])
}

func TestMergeEnvInterpolatesWindowsKeysCaseInsensitively(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment keys are case-insensitive")
	}

	merged := MergeEnv(
		[]string{`Path=C:\Windows\System32`},
		nil,
		[]string{`PATH=C:\tools;${PATH}`},
	)

	assert.Equal(t, `C:\tools;C:\Windows\System32`, merged["PATH"])
	assert.NotContains(t, merged, "Path")
}

func TestNewRunningTaskUsesMergedEnvForCommandArgsAndCwd(t *testing.T) {
	configDir := t.TempDir()
	taskConfig := &config.TaskConfig{
		Identifier: "task",
		ConfigDir:  configDir,
		Cwd:        "${WORK_DIR}",
		Run: config.RunCommands{
			{
				Command: "${COMMAND}",
				Args:    configutil.StringArray{"${ARG}"},
			},
		},
		Env: configutil.StringArray{
			"COMMAND=tool",
			"ARG=${FILE_ONLY}-task",
			"WORK_DIR=service",
		},
	}

	runningTask := NewRunningTask(
		taskConfig,
		zap.NewNop(),
		cmdwrap.NewWrapLogger([]string{"task"}),
		map[string]string{"FILE_ONLY": "file"},
		func(string, bool) {},
		util.NewPlatform(),
	)
	require.NotNil(t, runningTask)
	require.Len(t, runningTask.cmds, 1)

	assert.Equal(t, "file", runningTask.env["FILE_ONLY"])
	assert.Equal(t, "tool", runningTask.env["COMMAND"])
	assert.Equal(t, filepath.Join(configDir, "service"), runningTask.cwd)
	assert.Equal(t, "tool", runningTask.cmds[0].Executable())
	assert.Equal(t, []string{"file-task"}, runningTask.cmds[0].Args())
}
