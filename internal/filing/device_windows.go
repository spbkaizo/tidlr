package filing

// sameDevice is not implemented on Windows, where shares are drive letters or
// UNC paths rather than mount points; the check is skipped.
func sameDevice(a, b string) (same, ok bool) { return false, false }
