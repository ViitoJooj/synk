package transport

// windowsCandidates lists the fixed locations Tailscale installs its CLI into.
//
// The Windows installer does not add itself to PATH, so looking the binary up
// by bare name fails with "the system cannot find the file specified" — an
// error that reads like a missing config file rather than a missing program.
// Roots are taken from the environment rather than hardcoded so a non-default
// Windows installation still resolves.
func windowsCandidates(programFiles, programFilesX86 string) []string {
	var out []string
	for _, root := range []string{programFiles, programFilesX86} {
		if root == "" {
			continue // an empty root would produce a path rooted at nothing
		}
		out = append(out, root+`\Tailscale\tailscale.exe`)
	}
	return out
}
