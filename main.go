package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/muesli/termenv"
)

const (
	TagName = "Updated.At"
)

var (
	output = termenv.NewOutput(os.Stdout)
)

func ListEmacsStraightRepos() (repos []string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	repos, err = filepath.Glob(filepath.Join(home, ".emacs.d/straight/repos", "*"))
	return
}

// Create a new tag with name Updated.At or change its reference to ref
func CreateOrModifyGitTag(r *git.Repository, t string, ref *plumbing.Reference) (*plumbing.Reference, error) {
	tag, err := r.Tag(t)
	switch err {
	case nil: // CASE 1:  tag exists, set new reference of tag
		tag = plumbing.NewHashReference(tag.Name(), ref.Hash())
		if err = r.Storer.SetReference(tag); err != nil {
			return nil, err
		}
	case git.ErrTagNotFound: // CASE 2: tag does not exist, create a tag
		if tag, err = r.CreateTag(TagName, ref.Hash(), nil); err != nil {
			return nil, err
		}
	}
	return tag, nil
}

// Pull git changes and return true if the local workdir has updated
func PullGitChanges(r *git.Repository) (bool, error) {
	w, err := r.Worktree()
	if err != nil {
		return false, err
	}
	err = w.Pull(&git.PullOptions{})
	switch err {
	case git.NoErrAlreadyUpToDate:
		return false, nil
	default:
		return true, nil
	}

}

var commitBrief = `{{"\t"}}{{ .Committer.When.Format "2006-01-02" | Color "140" }} {{ slice .Hash.String 0 6 | Color "104"}} {{ Color "111" .Author.String }}
{{"\t"}}{{"\t"}}{{ replaceAll .Message "\n" "\n\t\t" | Color "108"}}
`

// Print git log to buffer, inspect commits since given time,
// count the number of commits and save to n
func GetGitLog(r *git.Repository, ref *plumbing.Reference, n *int) (string, error) {
	// KLUDGE use LogOptions.From doesn't work, use alternative method LogOptions.Since instead
	// cIter, err := r.Log(&git.LogOptions{From: tag.Hash(), Order: git.LogOrderDFSPost})
	var buf bytes.Buffer

	c, err := r.CommitObject(ref.Hash())
	if err != nil {
		return "", err
	}

	// KLUDGE hide the Updated.At tagged commit, show only after it
	t := c.Committer.When.Add(time.Second)
	cIter, err := r.Log(&git.LogOptions{Since: &t})
	if err != nil {
		return "", err
	}

	defer cIter.Close()

	tpl := template.New("tpl").
		Funcs(output.TemplateFuncs()).
		Funcs(template.FuncMap{"replaceAll": strings.ReplaceAll})
	tpl, err = tpl.Parse(commitBrief)
	if err != nil {
		return "", err
	}

	// process every single commit
	f := func(n *int) func(c *object.Commit) error {
		return func(c *object.Commit) error {
			*n++
			if err := tpl.Execute(&buf, c); err != nil {
				return err
			}
			return nil
		}
	}(n)
	err = cIter.ForEach(f)
	return buf.String(), err
}

var restartEmacsIsNeeded bool

func UpdateEmacsStraightRepo(p string, wg *sync.WaitGroup) {
	var (
		r         *git.Repository
		tag, head *plumbing.Reference
		rr        *git.Remote
		err       error
	)

	defer wg.Done()

	if r, err = git.PlainOpen(p); err != nil {
		log.Fatal(err)
	}
	if head, err = r.Head(); err != nil {
		log.Fatal(err)
	}
	if rr, err = r.Remote("origin"); err != nil {
		log.Fatal(err)
	}
	if tag, err = CreateOrModifyGitTag(r, TagName, head); err != nil {
		log.Fatal(err)
	}
	if _, err = PullGitChanges(r); err != nil {
		log.Fatal(err)
	}

	var (
		n int
		l string
	)
	l, err = GetGitLog(r, tag, &n)

	if n > 0 {
		restartEmacsIsNeeded = true
		fmt.Println(
			output.String("Fetched from", rr.Config().URLs[0]).Foreground(termenv.ANSIYellow),
			output.String(strconv.Itoa(n), "new commits").Foreground(output.Color("208")),
		)
		fmt.Println(output.String("local path:", p).Faint())
		fmt.Print(l)
	}
}

type ColoredWriter struct {
	c termenv.Color
}

func (c ColoredWriter) Write(p []byte) (n int, err error) {
	s := output.String(string(p)).Foreground(c.c)
	_, err = os.Stdout.WriteString(s.String())
	return len(p), err
}

func runCommand(s ...string) (err error) {
	cmd := exec.Command(s[0], s[1:]...)
	cmd.Stdout = ColoredWriter{c: output.Color("147")}
	cmd.Stderr = ColoredWriter{c: output.Color("175")}
	err = cmd.Run()
	output.Reset()
	return
}

func restartEmacs() {
	commands := []string{"emacsclient -e (kill-emacs)", "emacs -nw --daemon"}
	for _, v := range commands {
		err := runCommand(strings.Split(v, " ")...)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func main() {
	// walk trought emacs straight repos directories
	repos, err := ListEmacsStraightRepos()
	if err != nil {
		log.Fatal(err)
	}

	wg := &sync.WaitGroup{}
	for _, v := range repos {
		wg.Add(1)
		go UpdateEmacsStraightRepo(v, wg)
	}

	wg.Wait()
	if restartEmacsIsNeeded {
		restartEmacs()
	}
}
