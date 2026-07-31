//go:build linux

package proxy

func defaultResolver() OriginalDstResolver {
	return LinuxResolver{}
}
