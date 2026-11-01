// QStyleHints::colorScheme появился в Qt 6.5, а MIQT собран под API 6.4 —
// поэтому напрямую.
#include <QGuiApplication>
#include <QStyleHints>
#include "colorscheme.h"

extern "C" void mqColorSchemeChanged(int scheme);

int mq_color_scheme(void) {
	return static_cast<int>(QGuiApplication::styleHints()->colorScheme());
}

void mq_watch_color_scheme(void) {
	QObject::connect(QGuiApplication::styleHints(), &QStyleHints::colorSchemeChanged,
		[](Qt::ColorScheme scheme) { mqColorSchemeChanged(static_cast<int>(scheme)); });
}
