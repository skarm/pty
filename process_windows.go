package pty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func startConsoleProcess(console windows.Handle, command Command) (*os.Process, error) {
	//nolint:noctx // Process owns context cancellation; this Cmd is used only for path/environment preparation.
	cmd := exec.Command(command.Path, command.Args...) // #nosec G204 -- Starting the caller-specified executable is the API contract.

	cmd.Dir, cmd.Env = command.Dir, command.Env
	if cmd.Err != nil {
		return nil, cmd.Err
	}

	path := cmd.Path
	if !filepath.IsAbs(path) {
		if volume := filepath.VolumeName(path); volume != "" || strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") {
			return nil, errors.New("pty: drive-relative Windows executable paths require an absolute path")
		}

		path = filepath.Join(command.Dir, path)
	}

	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	path, err = exec.LookPath(path)
	if err != nil {
		return nil, err
	}

	application, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{command.Path}, command.Args...)))
	if err != nil {
		return nil, err
	}

	var directory *uint16

	if command.Dir != "" {
		dir, err := filepath.Abs(command.Dir)
		if err != nil {
			return nil, err
		}

		directory, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return nil, err
		}
	}

	// Environ drops empty entries, so reject them before normalization.
	if slices.Contains(command.Env, "") {
		return nil, errors.New("pty: environment entry must contain a name and '='")
	}

	environment, err := consoleEnvironment(cmd.Environ())
	if err != nil {
		return nil, err
	}

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	// PSEUDOCONSOLE takes the native HPCON value itself, unlike HANDLE_LIST.
	// This is a native pointer-sized token, not a pointer into the Go heap.
	if err := attachConsole(attributes, console); err != nil {
		return nil, err
	}

	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Null explicit standard handles let ConPTY supply its own. Otherwise
	// Windows can duplicate redirected parent handles despite inherit=false.
	startup.Flags = windows.STARTF_USESTDHANDLES

	var info windows.ProcessInformation
	// Adopt the process before resuming so a fast child cannot exit before
	// os.FindProcess acquires its own process handle. No handles are inherited.
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(application, line, nil, nil, false, flags, &environment[0], directory, &startup.StartupInfo, &info); err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(info.Process) }()
	defer func() { _ = windows.CloseHandle(info.Thread) }()

	process, err := os.FindProcess(int(info.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		return nil, err
	}

	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = process.Kill()
		_, _ = process.Wait()

		return nil, err
	}

	return process, nil
}

func consoleEnvironment(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return []uint16{0, 0}, nil
	}

	for _, value := range env {
		if strings.ContainsRune(value, 0) {
			return nil, errors.New("pty: environment contains NUL")
		}

		if consoleEnvironmentKey(value) == "" {
			return nil, errors.New("pty: environment entry must contain a name and '='")
		}
	}

	type entry struct{ key, value string }

	entries := make([]entry, len(env))
	capacity := 1

	for i, value := range env {
		// Fold each key once instead of allocating during sort comparisons.
		entries[i] = entry{key: strings.ToUpper(consoleEnvironmentKey(value)), value: value}
		capacity++

		for _, r := range value {
			capacity += utf16.RuneLen(r)
		}
	}

	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	// Use the exact UTF-16 capacity so Unicode-heavy values do not retain
	// excess space. Encode directly without joined strings or []rune.
	block := make([]uint16, 0, capacity)

	for _, entry := range entries {
		for _, r := range entry.value {
			block = utf16.AppendRune(block, r)
		}

		block = append(block, 0)
	}

	return append(block, 0), nil
}

// consoleEnvironmentKey keeps the leading '=' of Windows drive-directory keys.
func consoleEnvironmentKey(entry string) string {
	start := 0
	if strings.HasPrefix(entry, "=") {
		start = 1
	}

	end := strings.IndexByte(entry[start:], '=')
	if end < 1 {
		return ""
	}

	return entry[:start+end]
}

func attachConsole(attributes *windows.ProcThreadAttributeListContainer, console windows.Handle) error {
	// lpValue is HPCON itself here, not the address of a Go HANDLE variable.
	// Pass the native token as uintptr to avoid treating it as a Go pointer.
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")

	result, _, err := proc.Call(uintptr(unsafe.Pointer(attributes.List())), 0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0) // #nosec G103 -- Native attribute-list allocation remains valid until Delete.
	if result == 0 {
		return os.NewSyscallError("UpdateProcThreadAttribute", err)
	}

	return nil
}
