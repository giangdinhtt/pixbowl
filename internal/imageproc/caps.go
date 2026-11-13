package imageproc

/*
#cgo pkg-config: vips
#include <vips/vips.h>
#include <stdlib.h>

// vips_foreign_find_save_buffer returns the name of the saver that would handle
// this filename, or NULL when libvips was built without that format.
static int pixbowl_can_save(const char *name) {
	return vips_foreign_find_save_buffer(name) != NULL;
}
*/
import "C"

import "unsafe"

func canSave(suffix string) bool {
	name := C.CString("probe." + suffix)
	defer C.free(unsafe.Pointer(name))
	return C.pixbowl_can_save(name) != 0
}
