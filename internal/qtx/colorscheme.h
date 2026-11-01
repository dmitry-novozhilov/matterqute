#pragma once
#ifdef __cplusplus
extern "C" {
#endif

// Qt::ColorScheme: 0 — неизвестно, 1 — светлая, 2 — тёмная.
int mq_color_scheme(void);
// Подписка на QStyleHints::colorSchemeChanged; вызывает mqColorSchemeChanged.
void mq_watch_color_scheme(void);

#ifdef __cplusplus
}
#endif
