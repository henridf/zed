package compile

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/brimdata/zed/cmd/zed/dev"
	"github.com/brimdata/zed/cmd/zed/root"
	"github.com/brimdata/zed/compiler"
	"github.com/brimdata/zed/compiler/ast"
	"github.com/brimdata/zed/compiler/ast/dag"
	"github.com/brimdata/zed/compiler/parser"
	"github.com/brimdata/zed/lake/mock"
	"github.com/brimdata/zed/pkg/charm"
	"github.com/brimdata/zed/runtime/op"
	"github.com/brimdata/zed/zfmt"
	"github.com/peterh/liner"
)

var Cmd = &charm.Spec{
	Name:  "compile",
	Usage: "compile [ options ] zed",
	Short: "inspect Zed language abstract syntax trees and compiler stages",
	Long: `
The "zed dev compile" command parses a Zed expression and prints the resulting abstract syntax
tree as JSON object to standard output.  If you have installed the
shortcuts, "zc" is a short cut for the "zed dev compile" command.

"zed dev compile" is a tool for dev and test,
and is also useful to advanced users for understanding how Zed syntax is
translated into an analytics requests sent to the "zed server" search endpoint.

By default, it runs the built-in PEG parser built into this go binary.
If you specify -js, it will try to run a javascript version of the parser
by execing node in the currrent directory running the javascript in ./compiler/parser/run.js.

The -O flag is handy for turning on and off the compiler, which lets you see
how the parsed AST is transformed into a runtime object comprised of the
Zed kernel operators.
`,
	New: New,
}

func init() {
	dev.Cmd.Add(Cmd)
}

type Command struct {
	*root.Command
	repl     bool
	js       bool
	pigeon   bool
	proc     bool
	canon    bool
	semantic bool
	optimize bool
	parallel int
	layout   string
	n        int
	includes includes
}

func New(parent charm.Command, f *flag.FlagSet) (charm.Command, error) {
	c := &Command{Command: parent.(*root.Command)}
	f.BoolVar(&c.repl, "repl", false, "enter repl")
	f.BoolVar(&c.js, "js", false, "run javascript version of peg parser")
	f.BoolVar(&c.pigeon, "pigeon", false, "run pigeon version of peg parser")
	f.BoolVar(&c.proc, "proc", false, "run pigeon version of peg parser and marshal into ast.Proc")
	f.BoolVar(&c.semantic, "s", false, "display semantically analyzed AST (implies -proc)")
	f.BoolVar(&c.optimize, "O", false, "display optimized, non-filter AST (implies -proc)")
	f.IntVar(&c.parallel, "P", 0, "display parallelized AST (implies -proc)")
	f.BoolVar(&c.canon, "C", false, "display AST in Zed canonical format (implies -proc)")
	f.Var(&c.includes, "I", "source file containing Zed query text (may be repeated)")
	return c, nil
}

type includes []string

func (i includes) String() string {
	return strings.Join(i, ",")
}

func (i *includes) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func (c *Command) Run(args []string) error {
	_, cleanup, err := c.Init()
	if err != nil {
		return err
	}
	defer cleanup()
	if len(args) == 0 && len(c.includes) == 0 {
		return charm.NeedHelp
	}
	c.n = 0
	if c.js {
		c.n++
	}
	if c.pigeon {
		c.n++
	}
	if c.proc {
		c.n++
	}
	if c.semantic {
		c.n++
	}
	if c.optimize {
		c.n++
	}
	if c.parallel > 0 {
		c.n++
	}
	if c.n == 0 {
		if c.canon {
			c.proc = true
		} else {
			c.pigeon = true
		}
	}
	if c.repl {
		c.interactive()
		return nil
	}
	var src string
	if len(c.includes) > 0 {
		for _, path := range c.includes {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src += "\n" + string(b)
		}
	}
	src += strings.Join(args, " ")
	return c.parse(src)
}

func (c *Command) header(msg string) {
	if c.n > 1 {
		bars := strings.Repeat("=", len(msg))
		fmt.Printf("/%s\\\n", bars)
		fmt.Printf("|%s|\n", msg)
		fmt.Printf("\\%s/\n", bars)
	}
}

func (c *Command) parse(z string) error {
	if c.js {
		s, err := parsePEGjs(z)
		if err != nil {
			return err
		}
		c.header("pegjs")
		fmt.Println(s)
	}
	if c.pigeon {
		s, err := parsePigeon(z)
		if err != nil {
			return err
		}
		c.header("pigeon")
		fmt.Println(s)
	}
	if c.proc {
		p, err := compiler.ParseProc(z)
		if err != nil {
			return err
		}
		c.header("proc")
		c.writeProc(p)
	}
	if c.semantic {
		runtime, err := c.compile(z)
		if err != nil {
			return err
		}
		c.header("semantic")
		c.writeOp(runtime.Entry())
	}
	if c.optimize {
		runtime, err := c.compile(z)
		if err != nil {
			return err
		}
		if err := runtime.Optimize(); err != nil {
			return err
		}
		c.header("optimized")
		c.writeOp(runtime.Entry())
	}
	if c.parallel > 0 {
		runtime, err := c.compile(z)
		if err != nil {
			return err
		}
		if err := runtime.Optimize(); err != nil {
			return err
		}
		if err := runtime.Parallelize(c.parallel); err != nil {
			return err
		}
		c.header("parallelized")
		c.writeOp(runtime.Entry())
	}
	return nil
}

func (c *Command) writeProc(p ast.Proc) {
	s, err := procFmt(p, c.canon)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(s)
	}
}

func (c *Command) writeOp(op dag.Op) {
	s, err := dagFmt(op, c.canon)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(s)
	}
}

func (c *Command) compile(z string) (*compiler.Runtime, error) {
	p, err := compiler.ParseProc(z)
	if err != nil {
		return nil, err
	}
	return compiler.New(op.DefaultContext(), p, mock.NewLake(), nil)
}

const nodeProblem = `
Failed to run node on ./compiler/parser/run.js.  The "-js" flag is for PEG
development and should only be used when running "zed dev compile" in the root
directory of the Zed repository.`

func (c *Command) interactive() {
	rl := liner.NewLiner()
	defer rl.Close()
	for {
		line, err := rl.Prompt("> ")
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		rl.AppendHistory(line)
		if err := c.parse(line); err != nil {
			log.Println(err)
		}
	}
}

func runNode(dir, line string) ([]byte, error) {
	cmd := exec.Command("node", "./compiler/parser/run.js", "-e", "start")
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = strings.NewReader(line)
	return cmd.Output()
}

func normalize(b []byte) (string, error) {
	var v interface{}
	err := json.Unmarshal(b, &v)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(v, "", "    ")
	return string(out), err
}

func parsePEGjs(z string) (string, error) {
	b, err := runNode("", z)
	if err != nil {
		// parse errors don't cause this... this is only
		// caused by a problem running node.
		return "", errors.New(strings.TrimSpace(nodeProblem))
	}
	return normalize(b)
}

func procFmt(proc ast.Proc, canon bool) (string, error) {
	if canon {
		return zfmt.AST(proc), nil
	}
	procJSON, err := json.Marshal(proc)
	if err != nil {
		return "", err
	}
	return normalize(procJSON)
}

func dagFmt(op dag.Op, canon bool) (string, error) {
	if canon {
		return zfmt.DAG(op), nil
	}
	dagJSON, err := json.Marshal(op)
	if err != nil {
		return "", err
	}
	return normalize(dagJSON)
}

func parsePigeon(z string) (string, error) {
	ast, err := parser.Parse("", []byte(z))
	if err != nil {
		return "", err
	}
	goPEGJSON, err := json.Marshal(ast)
	if err != nil {
		return "", errors.New("go peg parser returned bad value for: " + z)
	}
	return normalize(goPEGJSON)
}
