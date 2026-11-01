package ui

import (
	"os"
	"testing"

	qt "github.com/mappu/miqt/qt6"
)

func TestZZMenuEmoji(t *testing.T) {
	onMain(func() {
		menu := qt.NewQMenu2()
		menu.QWidget.AddActionWithText("👍  :+1:")
		menu.QWidget.AddActionWithText("🎉  :tada:")
		menu.Popup(qt.NewQPoint2(10, 10))
		settle()
		menu.Grab().Save(os.Getenv("OUT") + "/menu.png")
		t.Logf("menu font families %v", menu.Font().Families())
		t.Logf("app QMenu font %v", qt.QApplication_FontWithClassName("QMenu").Families())
		t.Logf("app font %v", qt.QApplication_Font().Families())
		l := qt.NewQLabel3("👍  :+1:")
		l.Show()
		settle()
		l.Grab().Save(os.Getenv("OUT") + "/label.png")
		t.Logf("label font %v", l.Font().Families())
		menu.Close()
		l.Close()
	})
}
