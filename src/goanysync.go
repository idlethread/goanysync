// vim: set sts=4 ts=4 sw=4 et foldmethod=marker:
// Formated with: gofmt -w=true -tabwidth=4 -tabs=false

// main package of goanysync program by Esa Määttä <esa.maatta AT iki DOT fi>.
// Inspired by anything-sync-daemon written by graysky <graysky AT archlinux DOT us>
// Should be drop-in-replacement functionally wise, though doesn't use exactly same
// config file syntax.
package main


import (
    "errors"
    "flag"
    "fmt"
    "log"
    "os"
    "os/exec"
    "path"
    "regexp"
    "syscall"
    "time"
)

// mkdirAll creates a directory named path,
// along with any necessary parents, and returns nil,
// or else returns an error.
// The permission bits perm are used for all
// directories that mkdirAll creates. Also given uid and gid are set.
// If path is already a directory, mkdirAll does nothing
// and returns nil.
// This function is a copy of os.MkdirAll with uid and gid setting.
func mkdirAll(path string, perm os.FileMode, uid uint, gid uint) error {
    // If path exists, stop with success or error.
    dir, err := os.Stat(path)
    if err == nil {
        if dir.IsDir() {
            return nil
        }
        return &os.PathError{"mkdir", path, syscall.ENOTDIR}
    }

    // Doesn't already exist; make sure parent does.
    i := len(path)
    for i > 0 && os.IsPathSeparator(path[i-1]) { // Skip trailing path separator.
        i--
    }

    j := i
    for j > 0 && !os.IsPathSeparator(path[j-1]) { // Scan backward over element.
        j--
    }

    if j > 1 {
        // Create parent
        err = mkdirAll(path[0:j-1], perm, uid, gid)
        if err != nil {
            return err
        }
    }

    // Now parent exists, try to create.
    err = os.Mkdir(path, perm)
    if err != nil {
        // Handle arguments like "foo/." by
        // double-checking that directory doesn't exist.
        dir, err1 := os.Lstat(path)
        if err1 == nil && dir.IsDir() {
            return nil
        }
        return err
    }
    // Change user and group id
    if err1 := os.Chown(path, int(uid), int(gid)); err1 != nil {
        return err1
    }
    return nil
}

// exists checks whether given file name exists.
func exists(fn string) bool { // {{{
    if _, err := os.Stat(fn); err != nil {
        return !os.IsNotExist(err)
    }
    return true
} // }}}

// getFileUserAndGroupId returns owner user and group ids from given FileInfo.
func getFileUserAndGroupId(fi os.FileInfo) (uid uint, gid uint, err error) { // {{{
    if st, ok := fi.Sys().(*syscall.Stat_t); ok {
        return uint(st.Uid), uint(st.Gid), nil
    }
    err = errors.New("Stat failed on: " + fi.Name())
    return
} // }}}

// isValidSource checks whether given path name "s" is valid source for sync.
// Returns necessary information for sync/unsync function about "s".
func isValidSource(s string) (fi os.FileInfo, uid uint, gid uint, err error) { // {{{
    if fi, err = os.Stat(s); err != nil {
        return
    }

    if !fi.IsDir() {
        err = errors.New("Sync source path was not a directory: " + s)
        return
    }

    if uid, gid, err = getFileUserAndGroupId(fi); err != nil {
        return
    }

    return
} // }}}

// pathNameGen generates volatile and backup path names and a regex string for
// matching volatile path name.
func pathNameGen(s string, tmpfs string, uid, gid uint) (volatilePath, backupPath, volatilePathRe string) { // {{{
    volatilePrefix := path.Join(tmpfs, "goanysync-")
    const backupPostfix  string = "-backup_goanysync"

    volatileBasePathRe := fmt.Sprintf("%s[0-9]+:[0-9]+", volatilePrefix)
    volatilePathRe = path.Join(volatileBasePathRe, s)

    volatileBasePath := fmt.Sprintf("%s%d:%d", volatilePrefix, uid, gid)
    volatilePath = path.Join(volatileBasePath, s)

    backupPath = s + backupPostfix
    return
} // }}}

// Sync path locking to prevent synchronous operations
func getLock(lockName string) bool {
    return os.Mkdir(lockName, 0600) == nil
}

func releaseLock(lockName string) {
    if err := os.Remove(lockName); err != nil {
        log.Printf("releaseLock error: %s\n... This should not happen, panicing..", err)
        panic(err)
    }
}

// --------------------------------------------------------------------------

// checkAndFix checks if any sync sources where synced but not finally unsynced.
// Restores such sources from backup path to original state.
func checkAndFix(tmpfs string, syncSources *[]string) { // {{{
    for _, s := range *syncSources {
        _, backupPath, volatilePathRe := pathNameGen(s, tmpfs, 0, 0)

        vpMatch := func(p string, s string) bool {
            var match bool
            var err error
            if match, err = regexp.MatchString(p, s); err != nil {
                panic("Regexp matching error: " + err.Error())
            }
            return match
        }
        // Check if sync has already been called but tmpfs copy has been
        // deleted. This happens for example if computer boots before unsync is
        // called. In this case the 's' path is a broken symlink to the
        // volatilePath and the backupPath exists.
        if target, err := os.Readlink(s); err == nil && vpMatch(volatilePathRe, target) && !exists(target) && exists(backupPath) {
            os.Remove(s)
            os.Rename(backupPath, s)
        }
    }
    return
} // }}}

// sync replaces given paths in syncSources with symlinks to directories
// under given tmpfs path. Also it creates a backup directory for every
// syncSource path. If sync is called consecutively for same syncSources it
// syncs content from tmpfs paths to backup paths.
func sync(tmpfs string, syncSources *[]string, syncerBin string) { // {{{
    for _, s := range *syncSources {
        var (
            fi       os.FileInfo
            uid, gid uint
            err      error
        )

        if fi, uid, gid, err = isValidSource(s); err != nil {
            log.Printf("sync error: %s\n... Skipping path: %s", err, s)
            continue
        }

        // Volatile dirs name is based on orginal dir's name, uid and gid
        volatilePath, backupPath, _ := pathNameGen(s, tmpfs, uid, gid)

        // First check if our target directory in tmpfs is ready.
        // We must ensure that the original owner of the source directory can
        // read the tmpfs volatile target dir, so we use the originals
        // permissions.
        if err := mkdirAll(volatilePath, fi.Mode(), uid, gid); err != nil { // {{{
            log.Printf("sync error (volatile path creation): %s\n... Skipping path: %s", err, s)
            continue
        }   // }}}

        // Second check if we need to create initial backup and initial sync to
        // volatile
        if target, err := os.Readlink(s); err != nil || target != volatilePath { // {{{
            // TODO: don't ignore errors
            os.Rename(s, backupPath)
            if linkError := os.Symlink(volatilePath, s); linkError != nil {
                log.Printf("sync error (symlink): %s\n... Skipping path: %s", err, s)
                os.Rename(backupPath, s)
                continue
            }
            // Let's do initial sync to volatile
            cmd := exec.Command(syncerBin, "-a", backupPath + "/", s)
            if err := cmd.Run(); err != nil {
                log.Printf("sync error (volatile): %s\n... With command: %s\n... Skipping path: %s", err, cmd, s)
                os.Rename(backupPath, s)
            }
            continue
        }   // }}}

        // Everything was ready so we just sync from volatile tmpfs to backup
        cmd := exec.Command(syncerBin, "-a", "--delete", s + "/", backupPath)
        if err := cmd.Run(); err != nil { // {{{
            log.Printf("sync error (backup): %s\n... With command: %s\n... Sync to backup failed for: %s", err, cmd, s)
            continue
        }   // }}}
    }
    return
}   // }}}

// unsync removes symbolic linkin to tmpfs and restores original from backup
func unsync(tmpfs string, syncSources *[]string, removeVolatile bool) { // {{{
    for _, s := range *syncSources {
        var (
            uid, gid uint
            err      error
        )
        if _, uid, gid, err = isValidSource(s); err != nil {
            log.Printf("unsync error: %s\n... Skipping path: %s", err, s)
            continue
        }
        volatilePath, backupPath, _ := pathNameGen(s, tmpfs, uid, gid)

        // Check that backup path exists and is a directory
        if fi, err := os.Stat(backupPath); err != nil || !fi.IsDir() { // {{{
            log.Printf("unsync error (backup): %s\n... Skipping path: %s", err, s)
            continue
        }   // }}}

        // Check that "s" was symlink to the volatile path
        if target, err := os.Readlink(s); err != nil || target != volatilePath { // {{{
            log.Printf("unsync error (volatile): %s\n... Skipping path: %s", err, s)
            continue
        }   // }}}

        // Remove the link and replace it with backup
        // TODO: don't ignore errors
        os.Remove(s)
        os.Rename(backupPath, s)

        // XXX: Is there any reason to remove volatile target? Any other than
        // saving space?
        if removeVolatile {
            os.RemoveAll(volatilePath)
        }
    }
    return
} // }}}

func main() {
    const errorMessage string = "Error: non valid command provided."
    // Check that at least one argument given
    if len(os.Args) < 2 {
        log.Fatalln(errorMessage)
    }
    configFilePath := flag.String("c", "/etc/goanysync.conf", "Config file.")
    verbose := *flag.Bool("v", false, "Be more verbose.")
    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage of %s %s:\n", os.Args[0], "[options] <command>")
        fmt.Fprintf(os.Stderr, "  Commands:\n")
        fmt.Fprintf(os.Stderr, "   sync\t\tReplaces sync directories with symlinks to tmpfs and if already done syncs content from tmpfs to the backup.\n")
        fmt.Fprintf(os.Stderr, "   unsync\tRestores orginal state of sync directories.\n")
        fmt.Fprintf(os.Stderr, "   check\tChecks if sync was called without unsync before tmpfs was cleared.\n")
        fmt.Fprintf(os.Stderr, "   start\tAlias for running check and sync.\n")
        fmt.Fprintf(os.Stderr, "   stop\t\tAlias for running sync and unsync.\n")
        fmt.Fprintf(os.Stderr, "  Options:\n")
        flag.PrintDefaults()
    }
    flag.Parse()

    copts, err := ReadConfigFile(*configFilePath)
    if err != nil {
        log.Fatalln("Config file error:", err)
    }

    if verbose {
        copts.Print()
    }

    // For now don't allow synchronous runs at all, in one might lock per synch
    // dir, if such functionality would be needed.
    processLockFile := path.Join(copts.tmpfsPath, ".goanysync.lock")
    for !getLock(processLockFile) {
        time.Sleep(time.Millisecond*100)
    }
    defer releaseLock(processLockFile)

    switch flag.Arg(0) {
    case "check":
        checkAndFix(copts.tmpfsPath, &copts.syncPaths)
    case "sync":
        sync(copts.tmpfsPath, &copts.syncPaths, copts.syncerBin)
    case "unsync":
        unsync(copts.tmpfsPath, &copts.syncPaths, false)
    case "start":
        checkAndFix(copts.tmpfsPath, &copts.syncPaths)
        sync(copts.tmpfsPath, &copts.syncPaths, copts.syncerBin)
    case "stop":
        sync(copts.tmpfsPath, &copts.syncPaths, copts.syncerBin)
        unsync(copts.tmpfsPath, &copts.syncPaths, false)
    default:
        log.Println(errorMessage)
        fmt.Println()
        flag.Usage()
        os.Exit(1)
    }
    return
}
