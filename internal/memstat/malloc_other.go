//go:build !(linux && cgo)

package memstat

// MallocInUse — на этой платформе не узнать.
func MallocInUse() (bytes uint64, ok bool) { return 0, false }
