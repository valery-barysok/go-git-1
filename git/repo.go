// Package git wraps the git commandline tools.
// It extracts several common tasks that I need to perform when managing the
// horde of repositories that make up Crowbar, but can be used and extended
// to do other tasks.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigMap maps config keys to their values.
type ConfigMap map[string]string

// RefMap maps ref names into Ref structs.
type RefMap map[string]*Ref

// Repo is the main struct that we use to track Git repositories.
type Repo struct {
	// GitDir is the directory that the Git metadata is in for this repo.
	GitDir string
	// WorkDir is the directory that holds the working tree for this repo.
	WorkDir string
	// refs holds the cached RefMap.
	refs RefMap
	// cfg holds the cached config data.
	cfg ConfigMap
}

var gitCmd string
var statusRE *regexp.Regexp
var statMap = map[string]string{
	" ": "unmodified",
	"M": "modified",
	"A": "added",
	"D": "deleted",
	"R": "renamed",
	"C": "copied",
	"U": "unmerged",
	"?": "untracked",
	"!": "ignored",
}

func init() {
	var err error
	if gitCmd, err = exec.LookPath("git"); err != nil {
		panic("Cannot find git command!")
	}
	statusRE = regexp.MustCompile("^([ MADRCU!?])([ MADRCU?!]) (.*)$")
}

func findRepo(path string) (found bool, gitdir, workdir string) {
	stat, err := os.Stat(path)
	if err != nil {
		panic("Could not stat " + path)
	}
	if !stat.IsDir() {
		panic(path + " is not a directory!")
	}
	if strings.HasSuffix(path, ".git") {
		if stat, err = os.Stat(filepath.Join(path, "config")); err == nil {
			found = true
			gitdir = path
			workdir = ""
			return
		}
	}
	if stat, err = os.Stat(filepath.Join(path, ".git", "config")); err != nil {
		found = false
		return
	}
	found = true
	gitdir = filepath.Join(path, ".git")
	workdir = path
	return
}

// Open the first git repository that "owns" path.
func Open(path string) (repo *Repo, err error) {
	if path == "" {
		path = "."
	}
	path, err = filepath.Abs(path)
	basepath := path
	if err != nil {
		return
	}
	for {
		found, gitdir, workdir := findRepo(path)
		if found {
			repo = new(Repo)
			repo.GitDir = gitdir
			repo.WorkDir = workdir
			return
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return nil, errors.New(fmt.Sprintf("Could not find a Git repository in %s or any of its parents!", basepath))
}

// Git is a helper for creating exec.Cmd types and arranging to capture
// the output and erro streams of the command into bytes.Buffers
func Git(cmd string, args ...string) (res *exec.Cmd, stdout, stderr *bytes.Buffer) {
	cmdArgs := make([]string, 1)
	cmdArgs[0] = cmd
	cmdArgs = append(cmdArgs, args...)
	res = exec.Command(gitCmd, cmdArgs...)
	stdout, stderr = new(bytes.Buffer), new(bytes.Buffer)
	res.Stdout, res.Stderr = stdout, stderr
	return
}

// Git is a helper for making sure that the Git command runs in the proper repository.
func (r *Repo) Git(cmd string, args ...string) (res *exec.Cmd, out, err *bytes.Buffer) {
	var path string
	if r.WorkDir == "" {
		path = r.GitDir
	} else {
		path = r.WorkDir
	}
	res, out, err = Git(cmd, args...)
	res.Dir = path
	return
}

// Init initializes new Get metadata at the passed path.
// The rest of the args are passed to the 'git init' command unchanged.
func Init(path string, args ...string) (res *Repo, err error) {
	cmd, _, stderr := Git("init", append(args, path)...)
	if err = cmd.Run(); err != nil {
		return nil, errors.New(stderr.String())
	}
	res, err = Open(path)
	return
}

// Clone a new git repository.  The clone will be created in the current
// directory.
func Clone(source, target string, args ...string) (res *Repo, err error) {
	cmd, _, stderr := Git("clone", append(args, source, target)...)
	if err = cmd.Run(); err != nil {
		return nil, errors.New(stderr.String())
	}
	res, err = Open(target)
	return
}

// StatLine holds interesting bits of git status output.
type StatLine struct {
	indexStat, workStat, oldPath, newPath string
}

// StatLines is a slice of statuses.
type StatLines []*StatLine

// Print prints a StatLine in human readable format.
func (s *StatLine) Print() string {
	var res string
	if s.indexStat == "R" {
		res = fmt.Sprintf("%s was renamed to %s\n", s.oldPath, s.newPath)
	}
	res = res + fmt.Sprintf("%s is %s in the index and %s in the working tree.",
		s.newPath,
		statMap[s.indexStat],
		statMap[s.workStat])
	return res
}

func (r *Repo) mapStatus() (res StatLines) {
	var thisStat *StatLine
	cmd, out, err := r.Git("status", "--porcelain", "-z")
	if cmd.Run() != nil {
		panic(err.String())
	}
	for {
		line, err := out.ReadString(0)
		if err != nil {
			break
		}
		parts := statusRE.FindStringSubmatch(line)
		if parts != nil {
			if thisStat != nil {
				res = append(res, thisStat)
			}
			thisStat = new(StatLine)
			thisStat.indexStat = parts[1]
			thisStat.workStat = parts[2]
			thisStat.oldPath = parts[3]
			thisStat.newPath = parts[3]
		} else if thisStat != nil {
			thisStat.newPath = line
		} else {
			panic("Cannot happen!")
		}
	}
	if thisStat != nil {
		res = append(res, thisStat)
	}
	return
}

// IsClean checks to see if there are any uncomitted or untracked changes.
func (r *Repo) IsClean() (res bool, lines StatLines) {
	lines = r.mapStatus()
	res = len(lines) == 0
	return
}

// IsRaw checks to see if this is a raw repository.
func (r *Repo) IsRaw() (res bool) {
	return r.WorkDir == ""
}

// Path returns the best idea of the path to the repository.
// The exact value returned depends on whether this is a
// raw repository or not.
func (r *Repo) Path() (path string) {
	if r.IsRaw() {
		return r.GitDir
	}
	return r.WorkDir
}
