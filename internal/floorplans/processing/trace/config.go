// Package trace contains the tracing engines and their execution settings.
package trace

import "time"

// Limits stop a trace that isn't converging. Whichever is hit first ends
// the run; a valid trace by then is still published, marked as stopped.
type Limits struct {
	MaxTurns   int           `yaml:"maxTurns"`
	MaxSubmits int           `yaml:"maxSubmits"`
	MaxUSD     float64       `yaml:"maxUsd"`
	Timeout    time.Duration `yaml:"timeout"`
}

// Prices are US dollars per million tokens, for the spending cap. They
// default to Claude Opus 5.5's list prices; set them when using another model.
type Prices struct {
	Input      float64 `yaml:"input"`
	Output     float64 `yaml:"output"`
	CacheRead  float64 `yaml:"cacheRead"`
	CacheWrite float64 `yaml:"cacheWrite"`
}

// Tracer names identify the automatic and optional Claude engines.
const (
	TracerAuto   = "auto"
	TracerClaude = "claude"
)
