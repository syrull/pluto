package anthropic

// Models is the built-in catalog of selectable Claude models.
var Models = []string{
	"claude-opus-4-8",
	"claude-sonnet-5",
	"claude-haiku-4-5",
}

// DefaultModel is the model New callers should use when none is specified.
const DefaultModel = "claude-opus-4-8"

// DefaultJudgeModel is the small, cheap model used for auto-mode command review.
const DefaultJudgeModel = "claude-haiku-4-5"

// defaultContextWindow is the fallback context window for uncatalogued models.
const defaultContextWindow = 200_000

// modelContextWindows maps each model to its total context window in tokens.
var modelContextWindows = map[string]int{
	"claude-opus-4-8":   1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-sonnet-4-5": 200_000,
	"claude-haiku-4-5":  200_000,
}

// contextWindowFor returns the context window for a model id.
func contextWindowFor(model string) int {
	if w, ok := modelContextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}

// modelVision is the set of models that accept image input.
var modelVision = map[string]bool{
	"claude-opus-4-8":   true,
	"claude-sonnet-5":   true,
	"claude-sonnet-4-5": true,
	"claude-haiku-4-5":  true,
}

// visionFor reports whether a model accepts image (vision) input.
func visionFor(model string) bool { return modelVision[model] }

// Available returns the catalog. Implements the llm.Switchable contract.
func (p *Provider) Available() []string { return Models }

// thinkRegime classifies how a model accepts extended-thinking control.
type thinkRegime int

const (
	// regimeNone: the model has no usable extended-thinking control here; emit
	// no thinking field at all. Default for anything unrecognized.
	regimeNone thinkRegime = iota
	// regimeAdaptive: newer models. Use thinking:{type:"adaptive"} plus an
	// output_config.effort level; manual type:"enabled"+budget_tokens is a 400.
	regimeAdaptive
	// regimeLegacy: older models. Use thinking:{type:"enabled",budget_tokens};
	// effort/adaptive are unavailable.
	regimeLegacy
)

// modelThinkRegimes maps each catalog model to its thinking regime.
var modelThinkRegimes = map[string]thinkRegime{
	"claude-opus-4-8":   regimeAdaptive,
	"claude-sonnet-5":   regimeAdaptive,
	"claude-sonnet-4-5": regimeLegacy,
	"claude-haiku-4-5":  regimeLegacy,
}

// modelsWithXHigh is the set of models that accept the "xhigh" effort level.
var modelsWithXHigh = map[string]bool{
	"claude-opus-4-8": true,
	"claude-sonnet-5": true,
	// claude-opus-4-7 would belong here if added to the catalog.
}

// modelAdaptiveDefaultOn is the set of adaptive models where thinking is ON by default.
var modelAdaptiveDefaultOn = map[string]bool{
	"claude-sonnet-5": true,
}

// regimeFor returns the thinking regime for a model id.
func regimeFor(model string) thinkRegime { return modelThinkRegimes[model] }
