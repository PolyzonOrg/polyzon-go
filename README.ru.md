# Polyzon Go SDK

Официальный клиент на Go для [публичного API Polyzon](https://api.polyzon.org):
поиск 3D-моделей по 22 каталогам (Thingiverse, Printables, MakerWorld, Cults3D и
другим), полные карточки моделей, данные аккаунта и остаток квоты. Библиотека
использует только стандартную библиотеку Go, безопасна для одновременного
использования из многих горутин, сама повторяет запросы при временных сбоях и не
пускает токен в логи и тексты ошибок.

[English version](README.md)

- Документация API и песочница: https://api.polyzon.org
- Токены API: https://polyzon.org/account/ (раздел «API»)
- Справочник пакета: https://pkg.go.dev/github.com/PolyzonOrg/polyzon-go

## Содержание

- [Установка](#установка)
- [Быстрый старт](#быстрый-старт)
- [Авторизация](#авторизация)
- [Методы](#методы)
- [Постраничный обход](#постраничный-обход)
- [Пакетные запросы и параллельность](#пакетные-запросы-и-параллельность)
- [Метаданные ответа](#метаданные-ответа)
- [Ошибки](#ошибки)
- [Повторы](#повторы)
- [Таймауты и отмена](#таймауты-и-отмена)
- [Прокси](#прокси)
- [Свой HTTP-клиент, сертификаты УЦ](#свой-http-клиент-сертификаты-уц)
- [Логирование](#логирование)
- [Лимиты и квота](#лимиты-и-квота)
- [Справочник настроек](#справочник-настроек)
- [Безопасность](#безопасность)
- [Версии](#версии)
- [Требования](#требования)
- [Лицензия](#лицензия)

## Установка

```sh
go get github.com/PolyzonOrg/polyzon-go
```

## Быстрый старт

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/PolyzonOrg/polyzon-go"
)

func main() {
	client, err := polyzon.NewClient() // токен из POLYZON_API_KEY
	if err != nil {
		log.Fatal(err)
	}

	page, err := client.Search(context.Background(), polyzon.SearchParams{Query: "дракон"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("моделей:", page.Total)
	for _, item := range page.Items {
		fmt.Println(item.Source, item.ID, item.Title)
	}
}
```

Готовые программы лежат в [`examples/`](examples): `quickstart`, `pagination`,
`batch` и `proxy` (`go run ./examples/quickstart дракон`).

## Авторизация

Каждому запросу нужен личный токен API (`pzk_…`). Выпустите его на
https://polyzon.org/account/ в разделе «API». Клиент берёт токен из переменной
окружения `POLYZON_API_KEY` или из явной настройки:

```go
client, err := polyzon.NewClient(polyzon.WithAPIKey(os.Getenv("MY_POLYZON_TOKEN")))
```

`NewClient` вернёт ошибку, если токена нет, он длиннее 512 символов или содержит
что-то кроме видимых символов ASCII (пробелы, переводы строк и управляющие символы
запрещены, чтобы через токен нельзя было подставить заголовок). При печати клиента
токен маскируется (`polyzon.Client{baseURL: https://api.polyzon.org, apiKey: pzk_Q7xR…}`)
и никогда не попадает в ошибки и логи.

## Методы

Все методы первым аргументом принимают `context.Context`, их можно вызывать из
многих горутин одновременно.

### Search — поиск

```go
page, err := client.Search(ctx, polyzon.SearchParams{
	Query:    "кашпо",                 // обязательно, до 200 символов, по-русски или по-английски
	Page:     1,                       // 1–100, по умолчанию 1
	Size:     50,                      // 1–50, по умолчанию 20; Page × Size ≤ 2400
	Sort:     polyzon.SortPopular,     // relevant (по умолчанию), popular, newest, likes, makes, downloads
	Free:     true,                    // только бесплатные
	Sources:  []string{polyzon.SourceThingiverse, polyzon.SourcePrintables},
	Formats:  []string{"stl", "3mf"},
	Licenses: []string{polyzon.LicenseCC0, polyzon.LicenseCCBY},
	Lang:     polyzon.LangEN,          // ru (по умолчанию) или en; тексты на других языках переводятся машинно
})
fmt.Println(page.Total, page.TotalRelation, page.HasMore, page.Partial)
for _, item := range page.Items {
	fmt.Println(item.Source, item.ID, item.Title, item.URL, item.License)
	if item.Price != nil {
		fmt.Println(item.Price.Type, item.Price.Display)
	}
	if item.Stats.Likes != nil {
		fmt.Println(*item.Stats.Likes, "лайков")
	}
}
```

Нулевые значения не отправляются — действуют умолчания сервера. Списки уходят
одним параметром через запятую. `Partial` значит, что часть каталога не успела
ответить. `CorrectedQuery` и `DidYouMean` — подсказки по опечаткам.

Для площадок, лицензий, сортировок и языков есть константы (`polyzon.Source…`,
`polyzon.License…`, `polyzon.Sort…`, `polyzon.Lang…`), но все такие поля — обычные
`string`: новые площадки и лицензии появляются без нового выпуска SDK.

### GetModel — карточка модели

```go
model, err := client.GetModel(ctx, polyzon.SourceThingiverse, "tv-1234567", polyzon.WithLang(polyzon.LangEN))
fmt.Println(model.Title, model.Description, model.Category)
fmt.Println(model.Images)         // адреса на CDN самой площадки
if model.License != nil {
	fmt.Println(model.License.ID, model.License.Raw)
}
for _, other := range model.Copies { // та же модель на других площадках
	fmt.Println(other.Source, other.ID)
}
```

`source` и `id` — та же пара, что в результатах поиска и в адресе
`polyzon.org/model/{source}/{id}`. Они проверяются до запроса (source
`^[a-z0-9]{1,32}$`, id `^[A-Za-z0-9._-]{1,255}$`, не `.` и не `..`); неверное
значение даёт ошибку без запроса и без траты квоты. Модель, удалённая по
требованию правообладателя, отвечает `polyzon.ErrGone` — удалите её и у себя.

### Me — аккаунт и токен

```go
me, err := client.Me(ctx)
fmt.Println(me.Account.ID, me.Account.DisplayName, me.Token.Name, me.Token.Prefix)
```

### Limits — остаток лимитов

```go
limits, err := client.Limits(ctx)
fmt.Printf("%d/%d за минуту, %d/%d за сутки, сброс в %v\n",
	limits.PerMinute.Used, limits.PerMinute.Limit,
	limits.Daily.Used, limits.Daily.Limit, time.Unix(limits.Daily.ResetAt, 0))
```

`Me` и `Limits` суточную квоту не тратят.

Счётчики (`Stats.Likes`, `Stats.Downloads`, …) имеют тип `*int64`: `nil` значит,
что площадка это число не сообщает. Необязательные тексты (`Thumbnail`, `Creator`,
`Description`, …) равны `""`, если неизвестны. Даты — строки ISO-8601 ровно в том
виде, в каком их дала площадка. Незнакомые поля JSON игнорируются, а значения
неверного типа читаются как пустые, так что новые поля API не ломают ни сборку,
ни разбор ответа.

## Постраничный обход

`SearchAll` возвращает `iter.Seq2[polyzon.SearchItem, error]` и лениво загружает
страницы, начиная с `params.Page` (по умолчанию 1), до последней:

```go
for item, err := range client.SearchAll(ctx, polyzon.SearchParams{Query: "робот", Size: 50}) {
	if err != nil {
		return err // выдаётся один раз, после этого обход завершается
	}
	fmt.Println(item.Title)
	if enough() {
		break // следующие страницы не запрашиваются
	}
}
```

Каждая страница тратит один запрос суточной квоты, поэтому берите `Size: 50` и
выходите через `break`, когда моделей достаточно. API отдаёт не больше 2400
результатов на запрос (Page × Size) и не дальше 100-й страницы, поэтому `SearchAll`
останавливается после 100-й страницы, даже если там ещё `HasMore` (размер меньше 24).

## Пакетные запросы и параллельность

`*polyzon.Client` неизменяем и безопасен для одновременного использования:
создайте один клиент на процесс и раздайте его горутинам. Запросы переиспользуют
пул keep-alive-соединений (по умолчанию 10 на хост, см. `WithMaxConnections`).
Отдельного асинхронного API в Go нет: запускайте вызовы в горутинах и управляйте
ими через контексты.

`GetModels` загружает много карточек с ограниченной параллельностью и возвращает
по результату на каждую ссылку в порядке входа:

```go
refs := []polyzon.ModelRef{{Source: "thingiverse", ID: "tv-1234567"}, {Source: "printables", ID: "pr-123456"}}
results, err := client.GetModels(ctx, refs, polyzon.WithConcurrency(4), polyzon.WithLang(polyzon.LangRU))
if err != nil {
	log.Fatal(err) // только неверные настройки, например concurrency < 1
}
for _, res := range results {
	if res.Err != nil {
		fmt.Println(res.Ref, res.Err) // ошибка конкретной модели
		continue
	}
	fmt.Println(res.Model.Title)
}
```

По умолчанию параллельность 4. Как только запрос падает с `ErrAuthentication`,
`ErrPermissionDenied` или `ErrQuotaExceeded`, ещё не начатые ссылки получают ту же
ошибку без запросов. Каждая загруженная модель тратит один запрос суточной квоты,
и на весь пакет действует минутный лимит (по умолчанию 20).

## Метаданные ответа

У каждого результата есть поле `Meta` (в JSON не кодируется):

```go
page, _ := client.Search(ctx, params)
fmt.Println(page.Meta.RequestID) // X-Request-ID — назовите его в обращении в поддержку
if rl := page.Meta.RateLimit; rl != nil { // СУТОЧНОЕ окно из заголовков x-ratelimit-*
	fmt.Println(rl.Limit, rl.Remaining, time.Unix(rl.ResetAt, 0))
}
```

## Ошибки

Ошибки, которыми ответил API, — значения `*polyzon.APIError`. Классифицируйте их
через `errors.Is` и сигнальные ошибки пакета, а подробности читайте через `errors.As`:

```go
model, err := client.GetModel(ctx, "thingiverse", "tv-1234567")
switch {
case errors.Is(err, polyzon.ErrNotFound):
	// такой модели нет
case errors.Is(err, polyzon.ErrGone):
	// удалена по требованию правообладателя — удалите и у себя
case errors.Is(err, polyzon.ErrQuotaExceeded):
	// суточная квота исчерпана до 00:00 UTC
case errors.Is(err, polyzon.ErrRateLimited):
	// минутный лимит не отпустил и после автоматических повторов
case err != nil:
	var apiErr *polyzon.APIError
	if errors.As(err, &apiErr) {
		log.Printf("статус %d, код %s, запрос %s: %s", apiErr.StatusCode, apiErr.Code, apiErr.RequestID, apiErr.Detail)
	}
}
```

| Сигнальная ошибка | Когда | Коды |
| --- | --- | --- |
| `ErrBadRequest` | HTTP 400 | `bad_request` (в `APIError.Param` — какой параметр), `page_too_deep` |
| `ErrAuthentication` | HTTP 401 | `unauthorized`, `invalid_token` |
| `ErrPermissionDenied` | HTTP 403 | `account_action_required`: примите действующие условия на polyzon.org/account |
| `ErrNotFound` | HTTP 404 | `not_found` |
| `ErrGone` | HTTP 410 | `gone` |
| `ErrRateLimited` | любой HTTP 429 | `rate_limited`, `quota_exceeded` |
| `ErrQuotaExceeded` | HTTP 429 `quota_exceeded` (совпадает и с `ErrRateLimited`) | `quota_exceeded` |
| `ErrServer` | HTTP 5xx | `unavailable`, `internal`, любые |
| `ErrConnection` | сбой сети: DNS, TCP, TLS, прокси, обрыв (и таймауты) | — |
| `ErrTimeout` | попытка не уложилась во время (совпадает и с `ErrConnection`) | — |
| `ErrInvalidResponse` | ответ 2xx — не объект JSON или больше 8 МиБ | — |

Прочие статусы (другие 4xx и 3xx — редиректы клиент не выполняет) дают
`*APIError` без сигнальной ошибки. Поля `APIError`: `StatusCode`, `Code`, `Title`,
`Detail`, `Param`, `RequestID`, `RetryAfter` (`time.Duration`, 0 если заголовка
нет) и `Header`. Текст ошибки:
`polyzon: 429 rate_limited: Не больше 20 запросов в минуту. (request id …)`. Если
тело ошибки не JSON (HTML-страница пограничного прокси), `Code` пуст, а `Detail`
равен `HTTP <статус>`; сырое тело в текст ошибки не попадает.

Коды названы константами `Code*`. Ошибки вашего контекста ни в какую сигнальную
ошибку не заворачиваются: они совпадают с `context.Canceled` или
`context.DeadlineExceeded`. Неверный аргумент (плохой id модели, неверные
настройки) — обычная ошибка, возвращаемая до запроса.

## Повторы

Запрос повторяется до 2 раз (`polyzon.WithMaxRetries(n)`, 0 — без повторов) при
сбоях соединения, таймаутах, HTTP 500, 502, 503, 504 и 429 `rate_limited`. При
`quota_exceeded` и прочих 4xx повторов нет. Пауза берётся из `Retry-After` сервера
(секунды или HTTP-дата); если сервер просит ждать дольше 60 секунд, клиент не ждёт
и сразу возвращает ошибку. Без заголовка пауза равна `min(8 с, 0,5 с × 2^попытка)`
со случайным множителем 0,5–1,0. Отмена контекста прерывает паузу.

## Таймауты и отмена

У каждой попытки общий предел 30 секунд, включая соединение (не больше 10 секунд)
и чтение ответа. Измените его через `polyzon.WithTimeout`. Попытка, не уложившаяся
во время, повторяется, а в итоге возвращается `ErrTimeout`. Общий срок на все
повторы задаётся контекстом:

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
page, err := client.Search(ctx, params) // по истечении errors.Is(err, context.DeadlineExceeded)
```

Отменённый или истёкший контекст никогда не повторяется.

## Прокси

Без настройки действуют стандартные переменные окружения:
`HTTPS_PROXY`/`https_proxy`, `HTTP_PROXY`/`http_proxy` и `NO_PROXY`/`no_proxy`
(локальные адреса через них не проксируются). Явный прокси их заменяет:

```go
proxyURL, _ := url.Parse("http://user:password@proxy.internal:3128")
client, err := polyzon.NewClient(polyzon.WithProxy(proxyURL))
```

Поддерживаются схемы `http://`, `https://` (TLS до прокси), `socks5://` и
`socks5h://` (имена разрешает прокси) — всё это умеет сам `net/http`. Логин и
пароль из адреса отправляются как Basic-авторизация прокси (с раскодированием
процентов) и не попадают в ошибки и логи. Запросы к API по https идут через
туннель CONNECT со сквозным TLS, поэтому прокси не видит токен.
`polyzon.WithProxy(nil)` отключает прокси, в том числе из окружения.

## Свой HTTP-клиент, сертификаты УЦ

`polyzon.WithHTTPClient` отправляет запросы через ваш `*http.Client` — например,
чтобы доверять корпоративному УЦ, использовать mTLS или трассировку:

```go
pool, _ := x509.SystemCertPool()
pool.AppendCertsFromPEM(corporateCAPEM)
client, err := polyzon.NewClient(polyzon.WithHTTPClient(&http.Client{
	Transport: &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{RootCAs: pool},
	},
}))
```

SDK работает с копией вашего клиента, которая никогда не выполняет редиректы.
Прокси и размер пула настраивайте на его транспорте (`WithProxy` и
`WithMaxConnections` с ним не сочетаются). Проверка сертификатов TLS включена
всегда: выключить её в SDK нельзя, а `*http.Transport` с `InsecureSkipVerify`
отклоняется. `client.CloseIdleConnections()` закрывает
простаивающие соединения пула — например, перед долгой паузой процесса.

## Логирование

Передайте `*slog.Logger`, чтобы писать запросы на уровне debug: метод, путь с
параметрами, статус, номер попытки, длительность и паузы повторов. Заголовки, а
значит и токен, в лог не попадают.

```go
logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
client, err := polyzon.NewClient(polyzon.WithLogger(logger))
```

## Лимиты и квота

Квоты считаются на аккаунт, а не на токен:

- 20 запросов в минуту на любой метод;
- 200 запросов в сутки UTC на `Search` и `GetModel` (считается каждая страница
  `SearchAll` и каждая модель `GetModels`). `Me` и `Limits` её не тратят.

Сервер списывает квоту до проверки параметров, поэтому даже отклонённый запрос
(400) стоит одного. Запрос сверх минутного лимита падает с `rate_limited` и
автоматически повторяется после `Retry-After`; исчерпанная суточная квота даёт
`quota_exceeded` (сброс в 00:00 UTC) без повторов. Остаток суточной квоты есть в
`Meta.RateLimit` любого результата и в `client.Limits(ctx)`.

## Справочник настроек

| Настройка | По умолчанию | Примечания |
| --- | --- | --- |
| `WithAPIKey(key)` | переменная `POLYZON_API_KEY` | обязательно |
| `WithBaseURL(url)` | переменная `POLYZON_BASE_URL`, иначе `https://api.polyzon.org` | только https; http только для `localhost`, `127.0.0.1`, `[::1]`; без логина, параметров и фрагмента |
| `WithTimeout(d)` | 30 с на попытку | соединение не дольше 10 с |
| `WithMaxRetries(n)` | 2 | 0 отключает повторы |
| `WithProxy(u)` | прокси из окружения | `nil` отключает прокси |
| `WithHTTPClient(c)` | встроенный транспорт | редиректы не выполняются |
| `WithMaxConnections(n)` | 10 | одновременных соединений с API |
| `WithUserAgent(s)` | — | дописывается к `polyzon-go/1.0.0 (go <версия>; <ОС>/<архитектура>)` |
| `WithLogger(l)` | без логов | уровень debug |

На вызов: `polyzon.WithLang(lang)` для `GetModel`/`GetModels`,
`polyzon.WithConcurrency(n)` для `GetModels`.

## Безопасность

- **Никогда не отдавайте токен в браузер или мобильное приложение.** Оттуда его
  может извлечь кто угодно. Храните токен на своём сервере и обращайтесь к API
  оттуда.
- Клиент не принимает адреса API без https (кроме локальных — для тестов), не
  выполняет редиректы (редирект мог бы унести токен на чужой адрес), всегда
  проверяет сертификаты TLS, ограничивает тело ответа 8 МиБ и проверяет сегменты
  пути до сборки адреса.
- Утёкший токен сразу отзовите на https://polyzon.org/account/. Об уязвимостях
  пишите на security@polyzon.org, см. [SECURITY.md](SECURITY.md).

## Версии

SDK следует [семантическому версионированию](https://semver.org/lang/ru/).
Выпуски — это git-теги (`v1.0.0`); несовместимые изменения вышли бы новой
мажорной версией с путём модуля `/v2`. История изменений — в
[CHANGELOG.md](CHANGELOG.md).

## Требования

Go 1.23 или новее (нужны итераторы range-over-func). Зависимостей вне стандартной
библиотеки нет.

## Лицензия

[MIT](LICENSE) © 2026 Polyzon
