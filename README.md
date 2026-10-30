# Happ routing

Одна `geosite.dat` для доменов, одна `geoip.dat` для IP и небольшой профиль Happ.
Выбранные сервисы и списки блокировок идут через VPN, остальное — напрямую.

## Подключение

1. Откройте [ссылку импорта](https://raw.githubusercontent.com/kemko/happ-routing/release/happ-import-link.txt), скопируйте её и импортируйте в Happ как профиль маршрутизации.
2. Выберите профиль у нужной подписки, дождитесь загрузки геофайлов и переподключитесь.

Либо вставьте [готовый JSON](https://raw.githubusercontent.com/kemko/happ-routing/release/happ-routing.json) в [редактор Happ](https://routing.happ.su/ru) и импортируйте полученную ссылку.

Постоянные адреса баз:

```text
https://raw.githubusercontent.com/kemko/happ-routing/release/geosite.dat
https://raw.githubusercontent.com/kemko/happ-routing/release/geoip.dat
```

## Состав

| Категории GeoSite | Источник |
| --- | --- |
| `ai`, `ai-cn`, `gemini`, `youtube`, `twitter`, `tiktok`, `telegram`, `cloudflare`, `kinopub`, `private` | Соответствующие категории MetaCubeX |
| `rkn-domains` | RussiaFancyLists: `blacklist/domains/full-sld.lst` |
| `foreign-geoblock` | RussiaFancyLists: `geoblock/full-sld.lst` |

| Категории GeoIP | Источник |
| --- | --- |
| `twitter`, `telegram`, `cloudflare`, `private` | Соответствующие категории MetaCubeX |
| `rkn-ip` | RussiaFancyLists: `blacklist/ipsets/full.lst` |

AI используется целиком, включая китайские сервисы. Все конкретные URL находятся в [sources.json](sources.json).

В [happ-routing.json](happ-routing.json) сохранены ручные исключения:

- `DirectSites`: `alib.ru` и приватные домены.
- `ProxySites`: категории и 30 дополнительных доменов пользователя.
- `DirectIp`: приватные адреса.
- `ProxyIp`: категории Twitter, Telegram, Cloudflare и РКН.

`domain:example.com` включает сам домен и поддомены. Порядок `direct-proxy-block` даёт прямым исключениям приоритет над списками VPN. Блокировка рекламы не включена.

## Обновление

GitHub Actions ежедневно в 03:17 UTC (06:17 по Москве) скачивает все списки заново, проверяет их, собирает базы и публикует результат в ветку `release`. Также есть ручной запуск **Actions → Update geodata → Run workflow**. Изменения источников, профиля и сборщика запускают сборку автоматически.

В `main` хранится код. В `release` каждая изменившаяся сборка добавляется обычным коммитом: история файлов сохраняется, изменения можно сравнивать между коммитами. История начинается с текущего состояния ветки; ранее перезаписанные сборки автоматически не восстанавливаются. Не редактируйте её вручную. При ошибке загрузки или разбора публикация не выполняется и предыдущая сборка остаётся доступной.

Домены с DNS-меткой длиннее 63 байт или полным именем длиннее 253 байт пропускаются с предупреждением в логе: такие имена не могут использоваться в DNS. Остальные ошибки формата и списки без допустимых записей останавливают сборку.

Каждая сборка публикует исходные скачанные списки в `sources/`, их URL, количество записей и SHA-256 в `manifest.json`, а также контрольные суммы готовых файлов в `SHA256SUMS`.

Частота сборки и частота обновления в Happ различаются: согласно [документации Happ](https://github.com/HappDev/happ_su/blob/main/dev-docs/routing.md), клиент ограничивает обновление геофайлов. CI не меняет настройки уже установленного приложения.

Полный список РКН может сделать GeoSite большим. Списки `full-sld` используются как предоставлены источником: если в них попадает широкий доменный суффикс, он тоже пойдёт через VPN. Ручные прямые исключения имеют приоритет.

## Локальная сборка

Нужны Go версии из `go.mod` и Python 3. В каталоге репозитория:

```sh
go test ./...
python3 -m unittest discover -s scripts
go run .
python3 scripts/profile.py
```

Результат появится в `dist/`. Для изменения политики редактируйте `happ-routing.json`, для изменения источников — `sources.json`.

## Источники и лицензии

- [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) — GPL-3.0.
- [Noktomezo/RussiaFancyLists](https://github.com/Noktomezo/RussiaFancyLists) — MIT.

Копии лицензий и уведомлений находятся в [LICENSE](LICENSE), [NOTICE](NOTICE) и `licenses/`. Код и объединённые данные распространяются под GPL-3.0; исходные данные сохраняют уведомления их авторов.
