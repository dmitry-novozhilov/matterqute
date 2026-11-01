# Лента: HTML 4 для QTextBrowser

Вся лента собрана в один документ `QTextBrowser`, поэтому текст можно выделять сразу в нескольких сообщениях. В документе работают только свойства из подмножества Qt rich text:

- без `border-radius`, без внутренних отступов у инлайн-фрагментов, без `border-collapse`;
- блоки собираются из ячеек таблицы;
- реакции, «+» и превью — картинки (`<img>`), их рисует код через `QTextDocument::addResource`.

Ниже даны значения для светлой темы. Для тёмной подставьте значения из `tokens.json → color.dark`.

## Сообщение

Каждое сообщение — строка таблицы на всю ширину. Отступы группы задают поля ячейки, подсветку «упоминает меня» — фон ячейки.

```html
<table width="100%" cellspacing="0" cellpadding="0">
  <tr>
    <!-- колонка аватара: только у первого сообщения группы -->
    <td width="58" valign="top" style="padding:6px 0 4px 16px">
      <img src="avatar:carol" width="32" height="32">
    </td>
    <td valign="top" style="padding:6px 16px 4px 0" bgcolor="transparent | #fdf6e0 (упоминает меня)">
      <a href="mm-user:carol" style="color:#211d18; font-weight:600; text-decoration:none">carol</a>
      &nbsp;<span style="font-family:'PT Mono'; font-size:12px; color:#5f5a53"
                  title="2026-11-01 07:48:08">07:48</span><br>
      Да, всё ок, графики ровные.<br>
      Но p99 на /api/v4/posts подрос, гляньте
      <a href="https://grafana.example.com/d/abc" style="color:#007255">https://grafana.example.com/d/abc</a>
    </td>
  </tr>
</table>
```

- **Продолжение** — то же сообщение без строки автора: ячейка аватара пустая, верхнее поле 0. Сообщение считается продолжением, если автор тот же и прошло меньше 5 минут.
- **Изменено** — в конце текста `<span style="font-size:12px; color:#5f5a53"> (изменено)</span>`.
- **Системное** — `<span style="font-size:13px; font-style:italic; color:#5f5a53">dave joined the channel.</span>`, без строки автора.
- **Бот** — после имени `<span style="font-size:11px; font-weight:600; background:#eeebe6; color:#5f5a53">&nbsp;БОТ&nbsp;</span>`.
- **Ответ в канале** (без свёрнутых тредов) — над текстом `<span style="font-size:12px; color:#5f5a53">↳ Ответ в треде <b>dave:</b> @alice приложи туда…</span>`. Ссылка открывает тред.
- **Корень треда** — `<img src="icon:messages-square"> <a href="mm-thread:ID" style="color:#007255; font-weight:600">3 ответа</a> <span style="color:#5f5a53">последний 08:35</span>`.

## Фрагменты

| Что | HTML |
|---|---|
| Своё упоминание, `@channel`, `@all`, `@here` | `<a href="mm-user:alice" style="background:#f6dd90; color:#4f3005; font-weight:600; text-decoration:none">@alice</a>` |
| Чужое упоминание | `<a href="mm-user:bob" style="color:#007255; font-weight:600; text-decoration:none">@bob</a>` |
| ~канал | `<a href="mm-channel:town-square" style="color:#007255">~town-square</a>` |
| Код строкой | `<span style="font-family:'PT Mono'; font-size:13px; background:#eeebe6">make migrate</span>` |
| Кастомный эмодзи | `<img src="emoji:party-parrot" width="20" height="20">` |
| Сообщение из одних эмодзи | системный эмодзи шрифтом 32px |

## Блоки

**Цитата** — полоса 3 px из отдельной ячейки:
```html
<table cellspacing="0" cellpadding="0"><tr>
  <td width="3" bgcolor="#bcb6b0"></td>
  <td style="padding:0 10px; color:#5f5a53">не забудьте …</td>
</tr></table>
```

**Блок кода:**
```html
<table width="100%" cellspacing="0" cellpadding="8" bgcolor="#eeebe6"><tr><td>
  <pre style="font-family:'PT Mono'; font-size:13px">cache:
  ttl: 30s</pre>
</td></tr></table>
```

**Таблица** — сетку 1 px даёт фон таблицы, просвечивающий через `cellspacing="1"`. `border-collapse` не используем: в Qt 6.8 с ним пропадают все рамки.
```html
<table cellspacing="1" cellpadding="3" bgcolor="#d9d5cf">
  <tr><th bgcolor="#eeebe6" align="left">сервис</th><th bgcolor="#eeebe6" align="left">p99</th></tr>
  <tr><td bgcolor="#fcfbf9">api</td><td bgcolor="#fcfbf9">120 мс</td></tr>
</table>
```

**Чек-лист** — `QTextBlockFormat::setMarker(QTextBlockFormat::MarkerType::Checked / Unchecked)`.

**Вложение интеграции** (Grafana и др.) — полоса 4 px цвета вложения. Цвета `good`, `warning` и `danger` заменяются на `#428731`, `#b77610`, `#c13c36`, свой HEX от бота рисуется как есть.
```html
<table cellspacing="0" cellpadding="0" width="520"><tr>
  <td width="4" bgcolor="#c13c36"></td>
  <td bgcolor="#eeebe6" style="padding:8px 12px 10px">
    <a href="…" style="color:#007255; font-weight:600">[FIRING:1] DiskSpaceLow</a><br>
    Свободно <b>3%</b> на <span style="font-family:'PT Mono'; font-size:13px">/var/lib/postgresql</span>
    <table cellspacing="0" cellpadding="0" width="100%"><tr>  <!-- поля short: две колонки -->
      <td width="50%"><span style="font-size:12px; font-weight:600; color:#5f5a53">host</span><br>db-2</td>
      <td width="50%"><span style="font-size:12px; font-weight:600; color:#5f5a53">severity</span><br>critical</td>
    </tr></table>
    <span style="font-size:12px; color:#5f5a53">Grafana v11</span>
  </td>
</tr></table>
```
Если во вложении только fallback-текст, рисуется та же таблица, во второй ячейке — один текст.

**Файл** — ячейка с фоном `#eeebe6`, действия — ссылки:
```html
<table cellspacing="0" cellpadding="0"><tr><td bgcolor="#eeebe6" style="padding:8px 14px 8px 10px">
  <img src="icon:file-text" width="20" height="20"> <b>load-report.pdf</b>
  <span style="font-size:12px; color:#5f5a53">&nbsp;&nbsp;PDF · 1,2 МБ</span><br>
  <a href="mm-open:FILEID" style="color:#007255">Открыть</a> · <a href="mm-save:FILEID" style="color:#007255">Сохранить…</a>
</td></tr></table>
```
«Открыть» показывается только для безопасных типов. Для остальных — только «Сохранить…» и пометка «тип небезопасен для открытия».

**Картинка** — `<img src="preview:FILEID" width="W" height="H">`:
- размер не больше ширины ленты − 32 (− 42 с аватарами) и не больше 400 по высоте;
- маленькие картинки не увеличиваются;
- размер известен заранее из метаданных файла, поэтому место резервируется сразу;
- пока превью загружается, на его месте заглушка того же размера.

**GIF** — кадры подменяет `QMovie` в том же ресурсе. В углу метка «GIF».

**Видео** — в ленте только превью с кнопкой воспроизведения (48 px) и длительностью. Плеер открывается отдельным окном с `QVideoWidget`.

**Реакции** — каждая реакция картинкой `<a href="mm-react:👍"><img src="reaction:POSTID:👍"></a>`:
- размер 24 по высоте, скругление 12;
- чужая реакция: рамка `#d9d5cf`, фон `#fcfbf9`;
- своя: рамка `#007255`, фон `#d4f2e5`, текст `#003927`;
- наведение — подмена картинки по сигналу `highlighted(QUrl)`;
- кто поставил — подсказка `QToolTip`;
- «+» — картинка 32×24, по клику `QMenu` с 12 быстрыми эмодзи.

## Служебные строки

- **Разделитель дней** — `<table width="100%"><tr><td nowrap style="font-size:12px; font-weight:600; color:#5f5a53; padding:12px 10px 4px 16px">Сегодня, 1 ноября</td><td width="100%"><hr></td></tr></table>`. Цвет линии `#d9d5cf`.
- **Новые сообщения** — та же раскладка: подпись справа, линия и текст `#c13c36`.
- **Подгрузка истории** — строка «Загружаем историю…» по центру в начале документа.

## Ссылки-действия

Схемы ссылок, которые перехватывает `anchorClicked`:

| Схема | Действие |
|---|---|
| `mm-user:` | карточка пользователя |
| `mm-channel:` | открыть канал |
| `mm-thread:` | открыть тред |
| `mm-open:`, `mm-save:` | открыть или сохранить файл |
| `mm-react:` | поставить или снять реакцию |
| `mm-img:` | открыть картинку целиком |
| `mm-video:` | открыть плеер |
