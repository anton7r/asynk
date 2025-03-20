package cmdwrap

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/fatih/color"
)

type WrapLogger struct {
	taskIds         []string
	taskColors      map[string]*color.Color
	maxTaskIdLength int
}

func NewWrapLogger(taskIds []string) *WrapLogger {
	logger := &WrapLogger{taskIds: taskIds, taskColors: make(map[string]*color.Color, len(taskIds)), maxTaskIdLength: 0}

	for _, taskId := range taskIds {
		logger.taskColors[taskId] = color.RGB(generateRgbColor(taskId))
		if len(taskId) > logger.maxTaskIdLength {
			logger.maxTaskIdLength = len(taskId)
		}
	}

	return logger
}

func (logger *WrapLogger) log(taskId string, message string) {
	taskColor, ok := logger.taskColors[taskId]
	if !ok {
		logger.taskColors[taskId] = color.RGB(generateRgbColor(taskId))
		taskColor = logger.taskColors[taskId]
	}

	fmt.Println(taskColor.Sprintf("%s%s |", taskId, repeatString(" ", logger.maxTaskIdLength-len(taskId))), message)
}

func generateRgbColor(taskId string) (red, green, blue int) {
	h := fnv.New32()
	_, err := h.Write([]byte(taskId))
	if err != nil {
		panic(err)
	}
	hash := int(h.Sum32())

	red = (hash >> 16) & 0xff
	green = (hash >> 8) & 0xff
	blue = hash & 0xff

	return
}

func repeatString(s string, count int) string {
	builder := strings.Builder{}
	for i := 0; i < count; i++ {
		builder.WriteString(s)
	}
	return builder.String()
}
