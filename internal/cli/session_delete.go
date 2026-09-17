package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/digitalygo/smidja/internal/session"
)

var (
	errDeleteActive      = errors.New("session: refusing to delete the active session")
	errDeleteForeign     = errors.New("session: refusing to delete a session from another project")
	errDeleteSymlink     = errors.New("session: refusing to delete a symlinked session")
	errDeleteUnsupported = errors.New("session: deletion transaction unsupported on this platform")
)

var (
	deleteHookDuringConfirm func(*deleteCandidate)
	deleteHookBeforeOpen    func(*deleteCandidate)
	deleteHookAfterLock     func(*deleteCandidate)
	deleteHookBeforeUnlink  func(*deleteCandidate)

	deleteValidationStageHook func(stage string, candidate *deleteCandidate) error
)

const (
	deleteValidationParentHeld = "parent-held"
	deleteValidationRootOpen   = "root-open"
	deleteValidationTargetOpen = "target-open"
	deleteValidationTargetHeld = "target-held"
	deleteValidationLock       = "lock"
	deleteValidationLoad       = "load"
	deleteValidationCwd        = "cwd"
	deleteValidationPostLoad   = "post-load"
)

func runDeleteValidationStageHook(stage string, candidate *deleteCandidate) error {
	if deleteValidationStageHook == nil {
		return nil
	}
	return deleteValidationStageHook(stage, candidate)
}

type deleteCandidate struct {
	path            string
	id              string
	cwd             string
	info            os.FileInfo
	parent          string
	name            string
	canonicalDir    string
	canonicalParent string
	parentInfo      os.FileInfo
	lockPath        string
	lockInfo        os.FileInfo
	parentFile      *os.File
	parentRoot      *os.Root
	targetHeld      *os.File
}

func (c *deleteCandidate) Close() {
	if c == nil {
		return
	}
	if c.targetHeld != nil {
		_ = c.targetHeld.Close()
		c.targetHeld = nil
	}
	if c.parentFile != nil {
		_ = c.parentFile.Close()
		c.parentFile = nil
	}
	if c.parentRoot != nil {
		_ = c.parentRoot.Close()
		c.parentRoot = nil
	}
}

func validateDeleteCandidate(store *session.Store, cwd, path, activePath string) (result *deleteCandidate, err error) {
	candidate := &deleteCandidate{}
	defer func() {
		if err != nil {
			candidate.Close()
		}
	}()
	if err := deleteTransactionSupported(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("session: no store configured")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("session: empty delete path")
	}
	if sameFilePath(path, activePath) {
		return nil, errDeleteActive
	}
	dir, err := store.DirForCwd(cwd)
	if err != nil {
		return nil, err
	}
	canonicalDir, err := canonicalPath(dir)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute = filepath.Clean(absolute)
	parent := filepath.Dir(absolute)
	name := filepath.Base(absolute)
	if name == "." || name == string(filepath.Separator) || !filepath.IsLocal(name) {
		return nil, errDeleteForeign
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return nil, fmt.Errorf("session: inspect session directory: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errDeleteSymlink
	}
	if !parentInfo.IsDir() {
		return nil, errDeleteForeign
	}
	canonicalParent, err := canonicalPath(parent)
	if err != nil {
		return nil, err
	}
	if canonicalParent != canonicalDir {
		return nil, errDeleteForeign
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("session: inspect session file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errDeleteSymlink
	}
	candidate.path = absolute
	candidate.name = name
	candidate.parent = parent
	candidate.info = info
	candidate.canonicalDir = canonicalDir
	candidate.canonicalParent = canonicalParent
	candidate.parentInfo = parentInfo
	candidate.lockPath = absolute + ".lock"
	lockName := name + ".lock"
	if li, lerr := os.Lstat(candidate.lockPath); lerr == nil {
		if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
			return nil, errDeleteSymlink
		}
		candidate.lockInfo = li
	} else if !errors.Is(lerr, os.ErrNotExist) {
		return nil, fmt.Errorf("session: inspect session lock: %w", lerr)
	}
	parentFd, err := syscall.Open(parent, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, errDeleteSymlink
		}
		return nil, fmt.Errorf("session: open session directory: %w", err)
	}
	candidate.parentFile = os.NewFile(uintptr(parentFd), parent)
	if err := runDeleteValidationStageHook(deleteValidationParentHeld, candidate); err != nil {
		return nil, err
	}
	parentFileInfo, err := candidate.parentFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("session: inspect session directory: %w", err)
	}
	if !os.SameFile(parentInfo, parentFileInfo) {
		return nil, errors.New("session: session directory changed identity before deletion")
	}
	if !parentFileInfo.IsDir() {
		return nil, errDeleteSymlink
	}
	if err := runDeleteValidationStageHook(deleteValidationRootOpen, candidate); err != nil {
		return nil, err
	}
	candidate.parentRoot, err = os.OpenRoot(parent)
	if err != nil {
		return nil, fmt.Errorf("session: open session directory: %w", err)
	}
	dotInfo, err := candidate.parentRoot.Lstat(".")
	if err != nil {
		return nil, fmt.Errorf("session: inspect session directory: %w", err)
	}
	if !os.SameFile(parentFileInfo, dotInfo) {
		return nil, errors.New("session: session directory changed identity before deletion")
	}
	if err := runDeleteValidationStageHook(deleteValidationTargetOpen, candidate); err != nil {
		return nil, err
	}
	targetFd, err := syscall.Open(absolute, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, errDeleteSymlink
		}
		return nil, fmt.Errorf("session: inspect session file: %w", err)
	}
	candidate.targetHeld = os.NewFile(uintptr(targetFd), absolute)
	if err := runDeleteValidationStageHook(deleteValidationTargetHeld, candidate); err != nil {
		return nil, err
	}
	heldInfo, err := candidate.targetHeld.Stat()
	if err != nil {
		return nil, fmt.Errorf("session: inspect session file: %w", err)
	}
	if !os.SameFile(info, heldInfo) {
		return nil, errors.New("session: session file changed identity before deletion")
	}
	if heldInfo.Mode()&os.ModeSymlink != 0 || !heldInfo.Mode().IsRegular() {
		return nil, errDeleteSymlink
	}
	relInfo, err := candidate.parentRoot.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("session: inspect session file: %w", err)
	}
	if relInfo.Mode()&os.ModeSymlink != 0 || !relInfo.Mode().IsRegular() {
		return nil, errDeleteSymlink
	}
	if !os.SameFile(info, relInfo) {
		return nil, errors.New("session: session file changed identity before deletion")
	}
	if err := runDeleteValidationStageHook(deleteValidationLock, candidate); err != nil {
		return nil, err
	}
	if candidate.lockInfo != nil {
		relLock, err := candidate.parentRoot.Lstat(lockName)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("session: session lock changed identity before deletion")
			}
			return nil, fmt.Errorf("session: inspect session lock: %w", err)
		}
		if relLock.Mode()&os.ModeSymlink != 0 || !relLock.Mode().IsRegular() {
			return nil, errDeleteSymlink
		}
		if !os.SameFile(candidate.lockInfo, relLock) {
			return nil, errors.New("session: session lock changed identity before deletion")
		}
	} else {
		if _, err := candidate.parentRoot.Lstat(lockName); err == nil {
			return nil, errors.New("session: session lock changed identity before deletion")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("session: inspect session lock: %w", err)
		}
	}
	if err := runDeleteValidationStageHook(deleteValidationLoad, candidate); err != nil {
		return nil, err
	}
	loader, err := session.LoadWithOptions(absolute, session.LoadOptions{Strict: false})
	if err != nil {
		return nil, fmt.Errorf("session: delete candidate is not a session file: %w", err)
	}
	header := loader.Header()
	if header == nil {
		return nil, session.ErrNotASession
	}
	if err := runDeleteValidationStageHook(deleteValidationCwd, candidate); err != nil {
		return nil, err
	}
	canonicalCwd, err := canonicalPath(cwd)
	if err != nil {
		return nil, err
	}
	headerCwd, err := canonicalPath(header.Cwd)
	if err != nil || headerCwd != canonicalCwd {
		return nil, errDeleteForeign
	}
	if err := runDeleteValidationStageHook(deleteValidationPostLoad, candidate); err != nil {
		return nil, err
	}
	postLoad, err := candidate.parentRoot.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("session: inspect session file: %w", err)
	}
	if !os.SameFile(info, postLoad) {
		return nil, errors.New("session: session file changed identity before deletion")
	}
	candidate.id = header.ID
	candidate.cwd = header.Cwd
	return candidate, nil
}

func deleteSessionFile(store *session.Store, candidate *deleteCandidate) (err error) {
	if candidate == nil {
		return errors.New("session: no delete candidate")
	}
	if store == nil {
		candidate.Close()
		return errors.New("session: no store configured")
	}
	if err := deleteTransactionSupported(); err != nil {
		candidate.Close()
		return err
	}
	if candidate.parentFile == nil || candidate.parentRoot == nil || candidate.targetHeld == nil {
		candidate.Close()
		return errors.New("session: delete candidate lost its transaction handles")
	}
	if candidate.path == "" || candidate.name == "" || candidate.parent == "" {
		candidate.Close()
		return errors.New("session: delete candidate is incomplete")
	}
	if strings.HasSuffix(candidate.path, ".lock") {
		candidate.Close()
		return errDeleteSymlink
	}
	defer candidate.Close()

	if deleteHookDuringConfirm != nil {
		deleteHookDuringConfirm(candidate)
	}
	if deleteHookBeforeOpen != nil {
		deleteHookBeforeOpen(candidate)
	}
	if err := revalidateDeleteParent(candidate); err != nil {
		return err
	}
	if err := revalidateDeleteTarget(candidate); err != nil {
		return err
	}
	if err := revalidateDeleteLockBeforeAcquire(candidate); err != nil {
		return err
	}
	lock, err := acquireDeleteSidecarLock(candidate)
	if err != nil {
		return fmt.Errorf("session: acquire session lock: %w", err)
	}
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()
	if deleteHookAfterLock != nil {
		deleteHookAfterLock(candidate)
	}
	if err := revalidateDeleteParent(candidate); err != nil {
		return err
	}
	if err := revalidateDeleteTarget(candidate); err != nil {
		return err
	}
	if err := revalidateDeleteLockHeld(candidate, lock); err != nil {
		return err
	}
	if deleteHookBeforeUnlink != nil {
		deleteHookBeforeUnlink(candidate)
	}
	if err := revalidateDeleteParent(candidate); err != nil {
		return err
	}
	if err := revalidateDeleteTarget(candidate); err != nil {
		return err
	}
	if err := revalidateDeleteLockHeld(candidate, lock); err != nil {
		return err
	}
	if err := anchoredUnlink(candidate); err != nil {
		return fmt.Errorf("session: remove %q: %w", candidate.path, err)
	}
	return nil
}

func revalidateDeleteParent(c *deleteCandidate) error {
	freshParent, err := os.Lstat(c.parent)
	if err != nil {
		return fmt.Errorf("session: revalidate session directory: %w", err)
	}
	if freshParent.Mode()&os.ModeSymlink != 0 {
		return errDeleteSymlink
	}
	if !freshParent.IsDir() {
		return errDeleteForeign
	}
	if !os.SameFile(c.parentInfo, freshParent) {
		return errors.New("session: session directory changed identity before deletion")
	}
	heldParent, err := c.parentFile.Stat()
	if err != nil {
		return fmt.Errorf("session: revalidate session directory: %w", err)
	}
	if !os.SameFile(c.parentInfo, heldParent) {
		return errors.New("session: session directory changed identity before deletion")
	}
	dotInfo, err := c.parentRoot.Lstat(".")
	if err != nil {
		return fmt.Errorf("session: revalidate session directory: %w", err)
	}
	if !os.SameFile(c.parentInfo, dotInfo) {
		return errors.New("session: session directory changed identity before deletion")
	}
	canonicalParent, err := canonicalPath(c.parent)
	if err != nil {
		return err
	}
	if canonicalParent != c.canonicalDir || canonicalParent != c.canonicalParent {
		return errDeleteForeign
	}
	return nil
}

func revalidateDeleteTarget(c *deleteCandidate) error {
	fresh, err := os.Lstat(c.path)
	if err != nil {
		return fmt.Errorf("session: revalidate session file: %w", err)
	}
	if fresh.Mode()&os.ModeSymlink != 0 || !fresh.Mode().IsRegular() {
		return errDeleteSymlink
	}
	if !os.SameFile(c.info, fresh) {
		return errors.New("session: session file changed identity before deletion")
	}
	rel, err := c.parentRoot.Lstat(c.name)
	if err != nil {
		return fmt.Errorf("session: revalidate session file: %w", err)
	}
	if rel.Mode()&os.ModeSymlink != 0 || !rel.Mode().IsRegular() {
		return errDeleteSymlink
	}
	if !os.SameFile(c.info, rel) {
		return errors.New("session: session file changed identity before deletion")
	}
	heldInfo, err := c.targetHeld.Stat()
	if err != nil {
		return fmt.Errorf("session: revalidate session file: %w", err)
	}
	if !os.SameFile(c.info, heldInfo) {
		return errors.New("session: session file changed identity before deletion")
	}
	return nil
}

func revalidateDeleteLockBeforeAcquire(c *deleteCandidate) error {
	lockName := c.name + ".lock"
	freshLock, err := os.Lstat(c.lockPath)
	if c.lockInfo == nil {
		if err == nil {
			if freshLock.Mode()&os.ModeSymlink != 0 || !freshLock.Mode().IsRegular() {
				return errDeleteSymlink
			}
			return errors.New("session: session lock changed identity before deletion")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("session: revalidate session lock: %w", err)
		}
		if _, rerr := c.parentRoot.Lstat(lockName); rerr == nil {
			return errors.New("session: session lock changed identity before deletion")
		} else if !errors.Is(rerr, os.ErrNotExist) {
			return fmt.Errorf("session: revalidate session lock: %w", rerr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("session: revalidate session lock: %w", err)
	}
	if freshLock.Mode()&os.ModeSymlink != 0 || !freshLock.Mode().IsRegular() {
		return errDeleteSymlink
	}
	if !os.SameFile(c.lockInfo, freshLock) {
		return errors.New("session: session lock changed identity before deletion")
	}
	relLock, err := c.parentRoot.Lstat(lockName)
	if err != nil {
		return fmt.Errorf("session: revalidate session lock: %w", err)
	}
	if relLock.Mode()&os.ModeSymlink != 0 || !relLock.Mode().IsRegular() {
		return errDeleteSymlink
	}
	if !os.SameFile(c.lockInfo, relLock) {
		return errors.New("session: session lock changed identity before deletion")
	}
	return nil
}

func revalidateDeleteLockHeld(c *deleteCandidate, lock *deleteSidecarLock) error {
	lockInfo, err := lock.identity()
	if err != nil {
		return fmt.Errorf("session: revalidate session lock: %w", err)
	}
	lockName := c.name + ".lock"
	freshLock, err := os.Lstat(c.lockPath)
	if err != nil {
		return fmt.Errorf("session: revalidate session lock: %w", err)
	}
	if freshLock.Mode()&os.ModeSymlink != 0 || !freshLock.Mode().IsRegular() {
		return errDeleteSymlink
	}
	if !os.SameFile(lockInfo, freshLock) {
		return errors.New("session: session lock changed identity before deletion")
	}
	if c.lockInfo != nil && !os.SameFile(c.lockInfo, lockInfo) {
		return errors.New("session: session lock changed identity before deletion")
	}
	relLock, err := c.parentRoot.Lstat(lockName)
	if err != nil {
		return fmt.Errorf("session: revalidate session lock: %w", err)
	}
	if relLock.Mode()&os.ModeSymlink != 0 || !relLock.Mode().IsRegular() {
		return errDeleteSymlink
	}
	if !os.SameFile(lockInfo, relLock) {
		return errors.New("session: session lock changed identity before deletion")
	}
	return nil
}
