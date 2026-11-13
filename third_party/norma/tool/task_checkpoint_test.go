package tool

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestBackgroundEvidenceSurvivesManagerCleanup(t *testing.T) {
	t.Setenv("NORMA_TASK_OUTPUT_ROOT", t.TempDir())
	m, err := NewManager("persistent-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	job, err := m.Spawn(SpawnSpec{Command: "printf checkpoint-fixture", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := m.Wait(t.Context(), job.ID, time.Second)
	if !ok {
		t.Fatal("fixture did not exit")
	}
	m.Cleanup()
	data, err := os.ReadFile(info.OutputPath)
	if err != nil || !strings.Contains(string(data), "checkpoint-fixture") {
		t.Fatalf("output disappeared: %s %v", data, err)
	}
	if _, err = os.Stat(info.OutputPath + ".intent.json"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(info.OutputPath + ".result.json")
	if err != nil || !strings.Contains(string(data), "completed") {
		t.Fatalf("missing final checkpoint: %s %v", data, err)
	}
}
