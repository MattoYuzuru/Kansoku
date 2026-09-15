package claudeadapter

// Claude Code exports a metric stream alongside its log stream, and Kansoku
// used to discard all of it. The reason was not that the values were unsafe:
// it was that the metric lane was routed through the event lane, and a metric
// data point carries no event.name attribute. nativeEventName therefore fell
// back to the instrumentation-scope name, which is not a documented event, so
// every point was quarantined as an unknown schema.
//
// Metrics are dispatched by metric name instead. The vocabulary below is the
// closed set observed on the 2.1.220 wire
// (reports/artifacts/2026-08-01-component-audit/evidence/plugins/
// 03-claude-otlp-capture-strings.jsonl); an unrecognized metric name is still
// quarantined, exactly as an unrecognized event name is.
//
// These measurements are deliberately not merged into token_usage or
// model_operations. api_request stays the single counted operation surface;
// the metric stream is a second, independent observation of the same spend,
// which is what makes a disagreement between the two detectable at all.

// OTelMetricName is one real, documented Claude Code OTel metric name.
type OTelMetricName string

const (
	// MetricCostUsage is the session's accumulated cost in USD.
	MetricCostUsage OTelMetricName = "claude_code.cost.usage"
	// MetricTokenUsage counts tokens, split by a `type` attribute whose
	// observed values are input, output, cacheCreation and cacheRead.
	MetricTokenUsage OTelMetricName = "claude_code.token.usage"
	// MetricSessionCount counts CLI sessions started.
	MetricSessionCount OTelMetricName = "claude_code.session.count"
	// MetricActiveTime is accumulated active time in seconds -- the only
	// measurement of "time actually spent" any agent reports to Kansoku.
	MetricActiveTime OTelMetricName = "claude_code.active_time.total"
	// MetricEvents counts exported events and is the agent's own view of how
	// much it emitted, usable against Kansoku's received count.
	MetricEvents OTelMetricName = "com.anthropic.claude_code.events"
)

// DocumentedOTelMetrics is the closed metric vocabulary this recipe knows
// about. A name outside this list is quarantined, never guessed at.
func DocumentedOTelMetrics() []OTelMetricName {
	return []OTelMetricName{
		MetricCostUsage, MetricTokenUsage, MetricSessionCount,
		MetricActiveTime, MetricEvents,
	}
}

// DocumentedOTelMetric reports whether name is in the closed vocabulary.
func DocumentedOTelMetric(name OTelMetricName) bool {
	for _, documented := range DocumentedOTelMetrics() {
		if documented == name {
			return true
		}
	}
	return false
}

// MetricNameFromString resolves a wire metric name to the documented
// vocabulary member, reporting false for anything outside it.
func MetricNameFromString(name string) (OTelMetricName, bool) {
	candidate := OTelMetricName(name)
	if DocumentedOTelMetric(candidate) {
		return candidate, true
	}
	return "", false
}

// MetricAttribute names one real attribute observed on a Claude Code metric
// data point. These overlap with the log lane's attributes by design -- the
// agent stamps the same session and identity context on both streams -- and
// each maps onto a slot that already exists in OTLPSafeAttributes().
type MetricAttribute string

const (
	MetricAttributeSessionID    MetricAttribute = "session.id"
	MetricAttributeUserID       MetricAttribute = "user.id"
	MetricAttributeModel        MetricAttribute = "model"
	MetricAttributeTerminalType MetricAttribute = "terminal.type"
	MetricAttributeQuerySource  MetricAttribute = "query_source"
	MetricAttributeStartType    MetricAttribute = "start_type"
	// MetricAttributeTokenType splits claude_code.token.usage into its four
	// observed kinds. Without it the metric is a single opaque total and
	// cannot be compared against api_request's separate token columns.
	MetricAttributeTokenType MetricAttribute = "type"
)

// MetricAttributeSafeSlot maps a documented metric attribute onto the
// existing OTLPSafeAttributes() slot it belongs to. Anything not listed is
// dropped, exactly as on the log lane.
func MetricAttributeSafeSlot(attribute MetricAttribute) (string, bool) {
	switch attribute {
	case MetricAttributeSessionID:
		return "kansoku.session.id", true
	case MetricAttributeUserID:
		return "kansoku.user.id", true
	case MetricAttributeModel:
		return "kansoku.model.id", true
	case MetricAttributeTerminalType:
		return "kansoku.session.terminal_type", true
	case MetricAttributeQuerySource:
		return "kansoku.session.query_source", true
	case MetricAttributeStartType:
		return "kansoku.session.start_type", true
	case MetricAttributeTokenType:
		return "kansoku.metric.dimension", true
	default:
		return "", false
	}
}
