// Package mocksource — демо-источник данных (CLAUDE.md §2): собственный
// лёгкий сайт с сотнями фейковых объявлений вместо реальных коммерческих
// сайтов — чтобы демо не нарушало ничьих ToS и не выглядело инструментом
// для несанкционированного скрапинга. Намеренно ведёт себя как настоящий
// источник: отдаёт 429/5xx, тормозит, меняет разметку и иногда подсовывает
// страницу-заглушку вместо контента — чтобы pipeline было что обрабатывать.
//
// Это фикстура, не роль pipeline: запускается подкомандой `mock-source`.
package mocksource

import (
	"fmt"
	"math/rand"
)

// Listing — одно фейковое объявление. Генерируется ДЕТЕРМИНИРОВАННО по id:
// тот же id всегда даёт те же данные — на этом держатся тесты парсера
// (страница, отрисованная сервером, обязана разобраться обратно в ровно эти
// поля) и повторный опрос (тот же external_id — тот же объект, не новый).
type Listing struct {
	ID          int
	Title       string
	Price       int    // целое число денежных единиц
	Address     string // "" — у части объявлений адреса нет (Geocode → n/a)
	Description string
	PhotoNames  []string // пусто — у части объявлений фото нет (Media → n/a)
}

var (
	kinds   = []string{"Каркасный дом", "Кирпичный дом", "Газобетонный дом", "Таунхаус", "Гараж", "Баня", "Дача"}
	cities  = []string{"Киев", "Львов", "Одесса", "Днепр", "Харьков", "Винница"}
	streets = []string{"Садовая", "Лесная", "Центральная", "Речная", "Заводская", "Парковая", "Солнечная"}
	phrases = []string{
		"Участок ровный, коммуникации подведены.",
		"Рядом школа и остановка транспорта.",
		"Возможна рассрочка, документы готовы.",
		"Тихий район, хороший подъезд круглый год.",
		"Свежий ремонт, окна заменены.",
		"Есть место под парковку на две машины.",
	}
)

// Generate — детерминированно строит объявление по id.
func Generate(id int) Listing {
	r := rand.New(rand.NewSource(int64(id)*7919 + 13))

	l := Listing{
		ID:    id,
		Title: fmt.Sprintf("%s, %d м²", kinds[r.Intn(len(kinds))], 40+10*r.Intn(26)),
		Price: (50 + r.Intn(451)) * 10000,
	}
	if r.Intn(10) != 0 { // ~10% без адреса
		l.Address = fmt.Sprintf("г. %s, ул. %s, %d", cities[r.Intn(len(cities))], streets[r.Intn(len(streets))], 1+r.Intn(120))
	}
	l.Description = phrases[r.Intn(len(phrases))] + " " + phrases[r.Intn(len(phrases))]
	if r.Intn(7) != 0 { // ~14% без фото
		for n := 1 + r.Intn(4); n > 0; n-- {
			l.PhotoNames = append(l.PhotoNames, fmt.Sprintf("%d-%d.png", id, len(l.PhotoNames)+1))
		}
	}
	return l
}

// PhotoURLs — абсолютные URL фото относительно base ("http://host:port").
func (l Listing) PhotoURLs(base string) []string {
	out := make([]string, 0, len(l.PhotoNames))
	for _, n := range l.PhotoNames {
		out = append(out, base+"/photos/"+n)
	}
	return out
}
