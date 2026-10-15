package shareddomain

import "errors"

var (
	// ErrPermanent — сбой, который повтор не исправит (невалидные обязательные
	// поля, 4xx от источника, нераспознаваемая страница). Consumer'ы отправляют
	// такое сообщение сразу в DLQ, минуя retry-ступени: иначе оно проходило бы
	// все круги ожидания (1+2+4 мин) ради заведомо того же результата.
	// Domain оборачивает: fmt.Errorf("%w: ...", shareddomain.ErrPermanent).
	ErrPermanent = errors.New("permanent failure")

	// ErrNotFound — запрошенной сущности нет (item/source). Для HTTP — 404,
	// для consumer'а — тоже постоянный сбой (см. IsPermanent).
	ErrNotFound = errors.New("not found")
)

// IsPermanent — единственное место, где решается, что retry бессмысленен.
// ErrNotFound включён намеренно: сообщение ссылается на item, которого нет
// (job публикуется только после успешной записи), — повтор его не создаст.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrPermanent) || errors.Is(err, ErrNotFound)
}
