package cmdwrap

import (
	"github.com/anton7r/asynk/config"
	"github.com/anton7r/asynk/util/interpolation/idgen"
	"go.uber.org/zap"
)

// CommandFactory abstracts the creation of CommandWrapper slices.
// This allows tests to inject mock command factories that return
// controllable commands instead of spawning real OS processes.
type CommandFactory interface {
	// ParseAllCommands creates CommandWrappers for the given command strings.
	// Returns nil if any command fails to interpolate.
	ParseAllCommands(
		commands config.RunCommands,
		taskId string,
		log *zap.Logger,
		env map[string]string,
		cwd string,
		genIdInterpolator *idgen.GenIDInterpolator,
	) []*CommandWrapper
}

// DefaultCommandFactory creates real OS process commands using a ProcessManager.
type DefaultCommandFactory struct {
	procMgr ProcessManager
}

// NewDefaultCommandFactory creates a CommandFactory that spawns real OS processes.
func NewDefaultCommandFactory(procMgr ProcessManager) *DefaultCommandFactory {
	if procMgr == nil {
		procMgr = DefaultProcessManager()
	}
	return &DefaultCommandFactory{procMgr: procMgr}
}

func (f *DefaultCommandFactory) ParseAllCommands(
	commands config.RunCommands,
	taskId string,
	log *zap.Logger,
	env map[string]string,
	cwd string,
	genIdInterpolator *idgen.GenIDInterpolator,
) []*CommandWrapper {
	return ParseAllCommandsWithProcessManager(commands, taskId, log, env, cwd, genIdInterpolator, f.procMgr)
}
