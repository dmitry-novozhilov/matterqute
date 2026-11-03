#!/usr/bin/env python3
"""Заметки к релизу из заголовков коммитов между предыдущим тегом и этим.

Группы — по префиксу заголовка (см. CLAUDE.md, «Релизы»): «Новое:»/feat,
«Исправлено:»/fix, «Изменено:»/change, «Убрано:»/remove, «Документация:»/docs;
без префикса — «Прочее». Запуск: release-notes.py <тег> <owner/repo> > notes.md
"""

import re
import subprocess
import sys

# Префикс — слово (с необязательной областью в скобках) и двоеточие: «Новое: …», «fix(ui): …».
GROUPS = [
    ("Новое", r"^(новое|feat|feature)(\([^)]*\))?:"),
    ("Исправления", r"^(исправлено|исправление|fix|bugfix)(\([^)]*\))?:"),
    ("Изменения", r"^(изменено|изменение|change|changed|refactor|perf)(\([^)]*\))?:"),
    ("Убрано", r"^(убрано|remove|removed)(\([^)]*\))?:"),
    ("Документация", r"^(документация|docs?)(\([^)]*\))?:"),
]
PREFIX = re.compile(r"^[^:(]+(\([^)]*\))?:\s*")  # «fix(ui): …», «Исправлено: …»


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def main():
    tag, repo = sys.argv[1], sys.argv[2]
    try:
        prev = git("describe", "--tags", "--abbrev=0", tag + "^").strip()
    except subprocess.CalledProcessError:
        prev = ""
    rng = f"{prev}..{tag}" if prev else tag
    commits = []
    for line in git("log", "--no-merges", "--format=%h%x09%s", rng).splitlines():
        sha, _, subject = line.partition("\t")
        commits.append((sha, subject.strip()))

    sections = {title: [] for title, _ in GROUPS}
    sections["Прочее"] = []
    for sha, subject in commits:
        low = subject.lower()
        for title, pattern in GROUPS:
            if re.match(pattern, low):
                sections[title].append((sha, PREFIX.sub("", subject, count=1)))
                break
        else:
            sections["Прочее"].append((sha, subject))

    out = []
    for title, items in sections.items():
        if not items:
            continue
        out.append(f"## {title}\n")
        out.extend(f"- {text} ([{sha}](https://github.com/{repo}/commit/{sha}))" for sha, text in items)
        out.append("")
    if not commits:
        out.append("Изменений относительно предыдущего тега нет.\n")
    if prev:
        out.append(f"Полный список: [{prev}…{tag}](https://github.com/{repo}/compare/{prev}...{tag})\n")

    out.append("## Сборки\n")
    out.append("- **Linux x86_64** — `matterqute-…-linux-x86_64`: один бинарник (после скачивания `chmod +x`), "
               "собран на Debian 13 (Qt 6.8); нужны системные пакеты `libqt6widgets6 libqt6svg6 qt6-image-formats-plugins "
               "libqt6multimedia6` с Qt 6.8+ (Debian 13, Ubuntu 25.04+); `./matterqute --install` добавит ярлык в меню.")
    out.append("- **macOS (Apple Silicon)** — `matterqute-…-macos-arm64.dmg`: приложение не подписано — "
               "после копирования в «Программы» снять карантин: `xattr -dr com.apple.quarantine /Applications/Matterqute.app`.")
    out.append("- **Windows x86_64** — `matterqute-…-windows-x86_64-setup.exe`: установщик (без прав администратора, "
               "ярлык в меню, ссылки `mattermost://`); `…-windows-x86_64.zip` — то же без установки: распаковать и "
               "запустить `matterqute.exe`. Сборка не подписана, SmartScreen спросит подтверждение.")
    out.append("")
    print("\n".join(out))


if __name__ == "__main__":
    main()
