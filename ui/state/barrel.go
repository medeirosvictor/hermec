package state

// Barrel returns the scene coordinate shown at screen pixel (x, y) of a w x h
// view with barrel distortion strength k. It is the exact formula ui/crt.kage
// uses to sample the scene, so a click at (x, y) can be mapped to what the user
// sees there: u = 2x/w-1, v = 2y/h-1, f = 1 + k(u²+v²), result
// ((u·f+1)·w/2, (v·f+1)·h/2). k = 0 is the identity.
func Barrel(x, y, w, h, k float64) (float64, float64) {
	u := 2*x/w - 1
	v := 2*y/h - 1
	f := 1 + k*(u*u+v*v)
	return (u*f + 1) * w / 2, (v*f + 1) * h / 2
}
