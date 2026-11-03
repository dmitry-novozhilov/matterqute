#!/usr/bin/env bash
# MIQT ищет Qt через pkg-config. У Qt из официального установщика (aqtinstall)
# .pc-файлы либо есть с чужим prefix из сборочной машины Qt, либо их нет —
# тогда пишем свои для нужных модулей. Использование: qt-pkgconfig.sh <QT_ROOT_DIR>
set -euo pipefail
qt=${1:?QT_ROOT_DIR}
dir="$qt/lib/pkgconfig"
mkdir -p "$dir"
export PKG_CONFIG_PATH="$dir"

if ls "$dir"/Qt6Core.pc >/dev/null 2>&1; then
	sed -i "s|^prefix=.*|prefix=$qt|" "$dir"/*.pc
fi
if pkg-config --exists Qt6Widgets Qt6MultimediaWidgets Qt6SvgWidgets 2>/dev/null; then
	pkg-config --modversion Qt6Widgets
	exit 0
fi

version=$(basename "$(dirname "$qt")")
# модуль → от чего зависит
declare -A requires=(
	[Core]=""
	[Gui]="Qt6Core"
	[Widgets]="Qt6Gui Qt6Core"
	[Network]="Qt6Core"
	[PrintSupport]="Qt6Widgets"
	[Svg]="Qt6Gui"
	[SvgWidgets]="Qt6Svg Qt6Widgets"
	[Multimedia]="Qt6Network Qt6Gui Qt6Core"
	[MultimediaWidgets]="Qt6Multimedia Qt6Widgets"
	[OpenGL]="Qt6Gui"
	[OpenGLWidgets]="Qt6OpenGL Qt6Widgets"
	[DBus]="Qt6Core"
)
for m in "${!requires[@]}"; do
	[ -d "$qt/include/Qt$m" ] || continue
	cat > "$dir/Qt6$m.pc" <<EOF
prefix=$qt
includedir=\${prefix}/include
libdir=\${prefix}/lib

Name: Qt6 $m
Description: Qt $m module (pc written by qt-pkgconfig.sh)
Version: $version
Requires: ${requires[$m]}
Libs: -L\${libdir} -lQt6$m
Cflags: -I\${includedir} -I\${includedir}/Qt$m -fPIC
EOF
done
pkg-config --modversion Qt6Widgets Qt6MultimediaWidgets Qt6SvgWidgets
