package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anton7r/asynk/config"
	configutil "github.com/anton7r/asynk/config/util"
	"go.uber.org/zap"
)

const (
	rebuildSuppressionBenchmarkTaskID          = "build"
	rebuildSuppressionBenchmarkFiles           = 24
	rebuildSuppressionBenchmarkFuncs           = 32
	rebuildSuppressionBenchmarkSyntheticHashes = 16
)

var (
	rebuildSuppressionBenchmarkDigestSink    [sha256.Size]byte
	rebuildSuppressionBenchmarkScheduledSink int
	rebuildSuppressionBenchmarkFilesSink     int
)

type rebuildSuppressionGoBenchmarkFixture struct {
	compactFS      *rebuildSuppressionTestFS
	formattedFS    *rebuildSuppressionTestFS
	compactBytes   int64
	formattedBytes int64
	fileCount      int
}

func BenchmarkRebuildSuppressionGoFingerprintConstruction(b *testing.B) {
	fixture := newRebuildSuppressionGoBenchmarkFixture()
	modes := []struct {
		name string
		mode config.RebuildSuppressionMode
	}{
		{name: "size-and-hash", mode: config.RebuildSuppressionMode_SizeAndHash},
		{name: "language-aware-hash", mode: config.RebuildSuppressionMode_LanguageAwareHash},
	}

	for _, benchmarkMode := range modes {
		b.Run(benchmarkMode.name, func(b *testing.B) {
			taskConfig := benchmarkRebuildSuppressionTaskConfig(benchmarkMode.mode)
			effective := config.EffectiveRebuildSuppressionConfig{
				Enabled: true,
				Mode:    benchmarkMode.mode,
			}

			b.ReportAllocs()
			b.SetBytes(fixture.compactBytes)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fingerprint, stats, err := fingerprintTaskInputs(
					taskConfig,
					nil,
					effective,
					fixture.compactFS,
					nil,
				)
				if err != nil {
					b.Fatalf("fingerprint task inputs: %v", err)
				}
				if stats.TotalFiles != fixture.fileCount {
					b.Fatalf("fingerprinted %d files, expected %d", stats.TotalFiles, fixture.fileCount)
				}
				if benchmarkMode.mode == config.RebuildSuppressionMode_LanguageAwareHash &&
					stats.LanguageAwareFiles != fixture.fileCount {
					b.Fatalf("language-aware files = %d, expected %d", stats.LanguageAwareFiles, fixture.fileCount)
				}
				rebuildSuppressionBenchmarkFilesSink = len(fingerprint.Files)
			}

			b.ReportMetric(float64(fixture.fileCount), "files/op")
		})
	}
}

func BenchmarkRebuildSuppressionGoFormatOnlyEvent(b *testing.B) {
	fixture := newRebuildSuppressionGoBenchmarkFixture()
	syntheticRebuildPayload := benchmarkSyntheticRebuildPayload()
	modes := []struct {
		name              string
		mode              config.RebuildSuppressionMode
		expectedScheduled bool
	}{
		{
			name:              "size-and-hash",
			mode:              config.RebuildSuppressionMode_SizeAndHash,
			expectedScheduled: true,
		},
		{
			name:              "language-aware-hash",
			mode:              config.RebuildSuppressionMode_LanguageAwareHash,
			expectedScheduled: false,
		},
	}

	for _, benchmarkMode := range modes {
		b.Run(benchmarkMode.name, func(b *testing.B) {
			taskConfig := benchmarkRebuildSuppressionTaskConfig(benchmarkMode.mode)
			effective := config.EffectiveRebuildSuppressionConfig{
				Enabled: true,
				Mode:    benchmarkMode.mode,
			}
			acceptedFingerprint, stats, err := fingerprintTaskInputs(
				taskConfig,
				nil,
				effective,
				fixture.compactFS,
				nil,
			)
			if err != nil {
				b.Fatalf("fingerprint accepted inputs: %v", err)
			}
			if stats.TotalFiles != fixture.fileCount {
				b.Fatalf("fingerprinted %d files, expected %d", stats.TotalFiles, fixture.fileCount)
			}

			runner := NewRunnerWithDeps(
				benchmarkRebuildSuppressionConfig(taskConfig),
				zap.NewNop(),
				true,
				RunnerDeps{
					Platform:   testPlatform(),
					FS:         fixture.formattedFS,
					CmdFactory: &mockCommandFactory{},
				},
			)
			state := benchmarkRebuildSuppressionTaskState(runner)

			scheduledCount := 0
			b.ReportAllocs()
			b.SetBytes(fixture.formattedBytes)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				runner.rebuildSuppression.mu.Lock()
				state.LastAccepted = acceptedFingerprint
				runner.rebuildSuppression.mu.Unlock()

				scheduled := runner.shouldScheduleAfterRebuildSuppression(
					rebuildSuppressionBenchmarkTaskID,
					taskConfig,
				)
				if scheduled != benchmarkMode.expectedScheduled {
					b.Fatalf("scheduled = %v, expected %v", scheduled, benchmarkMode.expectedScheduled)
				}

				if scheduled {
					scheduledCount++
					benchmarkSyntheticRebuildWork(syntheticRebuildPayload)
				}
			}

			rebuildSuppressionBenchmarkScheduledSink = scheduledCount
			b.ReportMetric(float64(fixture.fileCount), "files/op")
			b.ReportMetric(float64(scheduledCount)/float64(b.N), "scheduled/op")
		})
	}
}

func newRebuildSuppressionGoBenchmarkFixture() rebuildSuppressionGoBenchmarkFixture {
	compactFS := newRebuildSuppressionTestFS()
	formattedFS := newRebuildSuppressionTestFS()
	compactFS.addDir("./pkg")
	formattedFS.addDir("./pkg")

	fixture := rebuildSuppressionGoBenchmarkFixture{
		compactFS:   compactFS,
		formattedFS: formattedFS,
		fileCount:   rebuildSuppressionBenchmarkFiles,
	}
	modTime := time.Unix(1_700_000_000, 0)

	for fileIndex := 0; fileIndex < rebuildSuppressionBenchmarkFiles; fileIndex++ {
		compact := benchmarkCompactGoSource(fileIndex, rebuildSuppressionBenchmarkFuncs)
		formatted := benchmarkFormattedGoSource(fileIndex, rebuildSuppressionBenchmarkFuncs)
		path := fmt.Sprintf("./pkg/file_%02d.go", fileIndex)

		compactFS.setFile(path, compact, modTime)
		formattedFS.setFile(path, formatted, modTime.Add(time.Second))
		fixture.compactBytes += int64(len(compact))
		fixture.formattedBytes += int64(len(formatted))
	}

	return fixture
}

func benchmarkRebuildSuppressionTaskConfig(mode config.RebuildSuppressionMode) *config.TaskConfig {
	enabled := true
	return &config.TaskConfig{
		Identifier: rebuildSuppressionBenchmarkTaskID,
		Type:       config.TasKType_Build,
		Include:    configutil.NewGlobArray("**/*.go"),
		RebuildSuppression: config.RebuildSuppressionConfig{
			Enabled: &enabled,
			Mode:    mode,
		},
	}
}

func benchmarkRebuildSuppressionConfig(taskConfig *config.TaskConfig) *config.Config {
	return &config.Config{
		Tasks: map[string]*config.TaskConfig{
			rebuildSuppressionBenchmarkTaskID: taskConfig,
		},
	}
}

func benchmarkRebuildSuppressionTaskState(runner *Runner) *rebuildSuppressionTaskState {
	runner.rebuildSuppression.mu.Lock()
	defer runner.rebuildSuppression.mu.Unlock()
	return runner.rebuildSuppressionTaskStateLocked(rebuildSuppressionBenchmarkTaskID)
}

func benchmarkCompactGoSource(fileIndex int, funcs int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "package pkg\n")
	for funcIndex := 0; funcIndex < funcs; funcIndex++ {
		fmt.Fprintf(&builder, "func F%d_%d(v int)int{\n", fileIndex, funcIndex)
		fmt.Fprintf(&builder, "if v%%2==0{return v+%d}\n", fileIndex+funcIndex+1)
		fmt.Fprintf(&builder, "return v-%d\n", fileIndex+funcIndex+1)
		fmt.Fprintf(&builder, "}\n")
		fmt.Fprintf(&builder, "var V%d_%d=F%d_%d(%d)\n", fileIndex, funcIndex, fileIndex, funcIndex, funcIndex)
	}
	return builder.String()
}

func benchmarkFormattedGoSource(fileIndex int, funcs int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "package pkg\n\n")
	for funcIndex := 0; funcIndex < funcs; funcIndex++ {
		fmt.Fprintf(&builder, "func F%d_%d(v int) int {\n", fileIndex, funcIndex)
		fmt.Fprintf(&builder, "\tif v%%2 == 0 {\n")
		fmt.Fprintf(&builder, "\t\treturn v + %d\n", fileIndex+funcIndex+1)
		fmt.Fprintf(&builder, "\t}\n")
		fmt.Fprintf(&builder, "\treturn v - %d\n", fileIndex+funcIndex+1)
		fmt.Fprintf(&builder, "}\n\n")
		fmt.Fprintf(&builder, "var V%d_%d = F%d_%d(%d)\n\n", fileIndex, funcIndex, fileIndex, funcIndex, funcIndex)
	}
	return builder.String()
}

func benchmarkSyntheticRebuildPayload() []byte {
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte((i*31 + 17) % 251)
	}
	return payload
}

func benchmarkSyntheticRebuildWork(payload []byte) {
	var digest [sha256.Size]byte
	for i := 0; i < rebuildSuppressionBenchmarkSyntheticHashes; i++ {
		digest = sha256.Sum256(payload)
	}
	rebuildSuppressionBenchmarkDigestSink = digest
}
