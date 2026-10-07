package ui

import (
	"github.com/gnome627/komar/internal/fx"
	"github.com/gnome627/komar/internal/i18n"
)

type helpSection struct {
	title string
	keys  [][2]string
}

func bannerSmall(m *Model) string { return fx.Banner(m.pal.RGB, true) }

func helpSections() []helpSection {
	if i18n.Current() == i18n.RU {
		return []helpSection{
			{"Навигация", [][2]string{
				{"1 2 3 4 5", "панели: контекст, неймспейс, ресурсы, связанные, главная"},
				{"tab  shift+tab", "следующая / предыдущая панель"},
				{"j k  ↑ ↓", "вверх / вниз"},
				{"g G", "в начало / в конец"},
				{"ctrl+d ctrl+u", "на полстраницы"},
				{"[ ]  ← →", "тип ресурса (панель 3) / вкладка (главная)"},
				{"/", "фильтр списка или поиск в тексте"},
				{"esc", "назад / сбросить фильтр"},
			}},
			{"Быстрый доступ", [][2]string{
				{"C", "выбрать контекст"},
				{"N", "выбрать неймспейс"},
				{"K", "все типы ресурсов, включая CRD"},
				{":", "команда kubectl (история, tab — дополнение)"},
				{"ctrl+r", "обновить всё"},
				{"?", "эта справка"},
				{"q  ctrl+c", "выход"},
			}},
			{"Действия с выбранным", [][2]string{
				{"enter", "под: шелл в отдельном окне; остальное: открыть"},
				{"x", "шелл пода в отдельном окне"},
				{"b  B", "kubectl debug (ephemeral-контейнер) здесь / в новом окне"},
				{"d", "удалить (с эффектом)"},
				{"D", "удалить принудительно (grace 0)"},
				{"s", "изменить число реплик"},
				{"+ -", "реплики ±1"},
				{"r", "rollout restart"},
				{"u", "история роллаута и откат"},
				{"l i y e o", "вкладки: логи, describe, yaml, события, вывод"},
				{"L", "логи по label selector"},
			}},
			{"Логи (главная панель)", [][2]string{
				{"/", "поиск regex"},
				{"n N", "следующее / предыдущее совпадение"},
				{"m", "режим: подсветка ↔ фильтр"},
				{"f  G", "следить за потоком"},
				{"p", "логи предыдущего контейнера"},
				{"t", "период: хвост, 5м, 15м, 1ч, 6ч, 24ч"},
				{"w", "перенос строк"},
				{"c", "очистить"},
			}},
			{"События (главная панель)", [][2]string{
				{"w", "только warning"},
				{"a", "все неймспейсы"},
				{"s", "сортировка: последние / количество / частота"},
				{"o", "группировка: по объекту / по причине"},
				{"enter", "перейти к объекту"},
			}},
			{"Роллаут (главная панель)", [][2]string{
				{"j k", "выбрать ревизию"},
				{"enter", "откатить на ревизию"},
			}},
		}
	}
	return []helpSection{
		{"Navigation", [][2]string{
			{"1 2 3 4 5", "panels: context, namespace, resources, related, main"},
			{"tab  shift+tab", "next / previous panel"},
			{"j k  ↑ ↓", "up / down"},
			{"g G", "top / bottom"},
			{"ctrl+d ctrl+u", "half page"},
			{"[ ]  ← →", "resource kind (panel 3) / tab (main)"},
			{"/", "filter list or search text"},
			{"esc", "back / clear filter"},
		}},
		{"Quick access", [][2]string{
			{"C", "pick context"},
			{"N", "pick namespace"},
			{"K", "all resource kinds, CRDs included"},
			{":", "kubectl command (history, tab completes)"},
			{"ctrl+r", "refresh everything"},
			{"?", "this help"},
			{"q  ctrl+c", "quit"},
		}},
		{"Actions on the selection", [][2]string{
			{"enter", "pod: shell in its own window; others: drill in"},
			{"x", "pod shell in its own window"},
			{"b  B", "kubectl debug (ephemeral container) here / new window"},
			{"d", "delete (with an effect)"},
			{"D", "force delete (grace 0)"},
			{"s", "scale"},
			{"+ -", "replicas ±1"},
			{"r", "rollout restart"},
			{"u", "rollout history and undo"},
			{"l i y e o", "tabs: logs, describe, yaml, events, output"},
			{"L", "logs by label selector"},
		}},
		{"Logs (main panel)", [][2]string{
			{"/", "regex search"},
			{"n N", "next / previous match"},
			{"m", "mode: highlight ↔ filter"},
			{"f  G", "follow the stream"},
			{"p", "previous container's logs"},
			{"t", "since: tail, 5m, 15m, 1h, 6h, 24h"},
			{"w", "wrap lines"},
			{"c", "clear"},
		}},
		{"Events (main panel)", [][2]string{
			{"w", "warnings only"},
			{"a", "all namespaces"},
			{"s", "sort: last seen / count / rate"},
			{"o", "group: by object / by reason"},
			{"enter", "jump to the object"},
		}},
		{"Rollout (main panel)", [][2]string{
			{"j k", "choose revision"},
			{"enter", "roll back to it"},
		}},
	}
}
