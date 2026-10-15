package core

// PipelineSpec — замороженный снимок "каким pipeline был и как был
// сконфигурирован" на конкретной версии. Строки pipeline_events (см. схему
// в CLAUDE.md §5) проставляют pipeline_version при записи — этот реестр
// делает такую строку интерпретируемой и через месяцы после того, как
// пороги/состав полей изменятся в следующей версии.
//
// В Go нет readonly/as const для структур — дисциплина держится на code
// review и этом комментарии: append, never edit — человеческое правило, не
// компиляторная гарантия.
type PipelineSpec struct {
	PublishedAt string
	Stages      []string // fetch, parse, media, geocode, ai, storage — активные на этой версии
	Breaker     struct {
		FailureThreshold int
		CooldownSeconds  int
	}
	ItemSchemaFields []string // поля items на этой версии — append-only список
	ExtendsFrom      string
	Delta            string // одно предложение разницы с предыдущей версией
}

// PipelineSpecs — append-only. Новые версии — только снизу; существующий
// ключ никогда не редактируется.
var PipelineSpecs = map[string]PipelineSpec{
	"1.0.0": {
		PublishedAt: "2026-09-22",
		Stages:      []string{"fetch", "parse", "media", "geocode", "ai", "storage"},
		Breaker: struct {
			FailureThreshold int
			CooldownSeconds  int
		}{FailureThreshold: CircuitBreakerFailureThreshold, CooldownSeconds: 30},
		ItemSchemaFields: []string{"title", "description", "price", "address", "blocked_reason"},
		ExtendsFrom:      "",
		Delta:            "Genesis spec — полный pipeline; geo/AI-поля заполняются более поздними СТАДИЯМИ, не более поздними ВЕРСИЯМИ.",
	},
}

// PipelineVersionOrder — карты (map) в Go не имеют порядка итерации,
// поэтому темпоральный порядок здесь нужно держать явным списком. Новые
// версии — в конец, в том же порядке, что и в PipelineSpecs.
var PipelineVersionOrder = []string{"1.0.0"}

// CurrentPipelineVersion — бампается вместе с новой записью в PipelineSpecs.
const CurrentPipelineVersion = "1.0.0"

// GetPipelineSpec возвращает замороженный спек и существует ли он — вызовы
// во время исторического replay (чтение старой строки pipeline_events)
// должны обрабатывать not-found, а не считать каждую проставленную версию
// до сих пор известной.
func GetPipelineSpec(version string) (PipelineSpec, bool) {
	s, ok := PipelineSpecs[version]
	return s, ok
}
