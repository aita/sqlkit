package decl

// resetRegisterTypeErrs clears the recorded invalid RegisterType calls, so a
// test exercising registry misuse can restore the package-global state and
// test order doesn't matter.
func resetRegisterTypeErrs() {
	dbTypeMu.Lock()
	defer dbTypeMu.Unlock()
	dbTypeErrs = nil
}

// ResetRegisterTypeErrs exposes resetRegisterTypeErrs to the external test
// package.
var ResetRegisterTypeErrs = resetRegisterTypeErrs
