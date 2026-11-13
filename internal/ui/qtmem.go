package ui

import (
	"runtime"
	"unsafe"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
)

// Память объектов Qt, созданных из Go (CLAUDE.md, «Память Qt»).
//
// Конструктор MIQT (qt.NewQ…) отдаёт объект без финализатора: не удалили —
// он живёт до конца процесса. То, что Qt возвращает по значению (картинка
// Scaled, QTextCursor, QVariant…), MIQT отдаёт с финализатором Go, но сборщик
// видит только обёртку в десятки байт, а не картинку за ней, и запускается
// редко — раз в 2 минуты; кадры ролика за это время занимали десятки
// гигабайт. И удаляет финализатор в своём потоке: QTextCursor при этом правит
// документ, который главный поток правит в то же время.
//
// Поэтому временное — через tmp, крупное и долгоживущее — free, когда стало
// не нужно.

// deleter — объект MIQT: у всех есть Delete и UnsafePointer (nil у nil).
type deleter interface {
	Delete()
	UnsafePointer() unsafe.Pointer
}

// pool — объекты tmp, ждущие удаления.
var pool struct {
	objs   []deleter
	queued bool
}

// tmp — временный объект: удалится в главном потоке, когда управление
// вернётся в цикл событий. Для аргументов, которые Qt копирует (цвет,
// прямоугольник, QVariant, шрифт, курсор документа…), и значений, нужных
// только до конца обработчика. Не хранить и не держать через вложенный цикл
// событий (QDialog.Exec, ProcessEvents): к его концу объекта уже нет.
func tmp[T deleter](obj T) T {
	if obj.UnsafePointer() == nil {
		return obj
	}
	if !mainthread.IsCurrent() {
		// Не главный поток Qt или приложения ещё нет — цикла событий, который
		// удалил бы объект, тоже: пусть удалит финализатор.
		runtime.SetFinalizer(obj, nil)
		runtime.SetFinalizer(obj, func(o T) { o.Delete() })
		return obj
	}
	pool.objs = append(pool.objs, obj)
	if !pool.queued {
		pool.queued = true
		mainthread.Start(drainPool)
	}
	return obj
}

func drainPool() {
	objs := pool.objs
	pool.objs, pool.queued = nil, false
	for _, o := range objs {
		release(o)
	}
}

// free удаляет объекты сразу; nil пропускает.
func free[T deleter](objs ...T) {
	for _, o := range objs {
		release(o)
	}
}

// release — Delete без финализатора MIQT: иначе объект удалится второй раз.
func release(o deleter) {
	if o == nil || o.UnsafePointer() == nil {
		return
	}
	runtime.SetFinalizer(o, nil)
	o.Delete()
}

// qcolor, qbrush, qpen — временные цвет, кисть и перо (см. tmp) для рисования.
func qcolor(name string) *qt.QColor { return tmp(qt.NewQColor6(name)) }

func qbrush(name string) *qt.QBrush { return tmp(qt.NewQBrush3(qcolor(name))) }

func qpen(name string, width float64) *qt.QPen { return tmp(qt.NewQPen4(qbrush(name), width)) }
