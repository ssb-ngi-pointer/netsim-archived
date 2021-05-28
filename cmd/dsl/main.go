package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	refs "go.mindeco.de/ssb-refs"
)

type Whoami struct {
	ID refs.FeedRef
}

type Latest struct {
	ID       string
	Sequence int
	TS       int
}

type Puppet struct {
	Port      int
	directory string
	feedID    string
	name      string
	seqno     int64
}

type TestError struct {
	err     error
	message string
}

func (t TestError) Error() string {
	return t.message
}

type Simulator struct {
	puppetMap       map[string]Puppet
	puppetDir       string
	portCounter     int
	instr           Instruction
	instructions    []Instruction
	basePort        int
	implementations map[string]string
}

func startPuppet(s Simulator, p Puppet, shim string) error {
	filename := filepath.Join(s.puppetDir, fmt.Sprintf("%s.txt", p.name))
	logfile, err := os.Create(filename)
	if err != nil {
		return TestError{err: err, message: "could not create log file"}
	}
	defer logfile.Close()
	var cmd *exec.Cmd
	// currently the simulator has a requirement that each language implementation folder must contain a sim-shim.sh file
	// sim-shim.sh contains logic for starting the corresponding sbot correctly.
	// e.g. reading the passed in ssb directory ($1) and port ($2)
	cmd = exec.Command(filepath.Join(s.implementations[shim], "sim-shim.sh"), p.directory, strconv.Itoa(p.Port))
	cmd.Stderr = logfile
	cmd.Stdout = logfile
	err = cmd.Run()
	if err != nil {
		return TestError{err: err, message: fmt.Sprintf("failure when creating puppet, see %s for information", filename)}
	}
	return nil
}

func makeSimulator(basePort int, puppetDir string, sbots []string) Simulator {
	puppetMap := make(map[string]Puppet)
	langMap := make(map[string]string)

	for _, bot := range sbots {
		botDir, err := filepath.Abs(bot)
		if err != nil {
			fmt.Println(err)
			continue
		}
		// index language implementations by the last folder name
		langMap[filepath.Base(botDir)] = botDir
	}

	absPuppetDir, err := filepath.Abs(puppetDir)
	if err != nil {
		log.Fatalln(err)
	}

	return Simulator{puppetMap: puppetMap, puppetDir: absPuppetDir, implementations: langMap, basePort: basePort}
}

func (s Simulator) getSrcPuppet() Puppet {
	return s.puppetMap[s.instr.getSrc()]
}

func (s Simulator) getDstPuppet() Puppet {
	return s.puppetMap[s.instr.getDst()]
}

func (s *Simulator) incrementPort() {
	s.portCounter += 1
}

func (s *Simulator) ParseTest(lines []string) {
	s.instructions = make([]Instruction, 0, len(lines))
	fmt.Println("## Start test file")
	for i, line := range lines {
		instr := parseTestLine(line, i+1)
		instr.Print()
		s.instructions = append(s.instructions, instr)
	}
	fmt.Println("## End test file")
}

func (s Simulator) evaluateRun(err error) {
	if err != nil {
		s.instr.TestFailure(err)
	} else {
		s.instr.TestSuccess()
	}
}

func (s *Simulator) updateCurrentInstruction(instr Instruction) {
	s.instr = instr
}

// TODO: add logic to check for port availability before tying port to puppet
func (s *Simulator) acquirePort() int {
	port := s.basePort + s.portCounter
	s.incrementPort()
	return port
}

func (s Simulator) execute() {
	for _, instr := range s.instructions {
		s.updateCurrentInstruction(instr)
		switch instr.command {
		case "start":
			name := instr.args[0]
			langImpl := instr.args[1]
			if _, ok := s.implementations[langImpl]; !ok {
				err := errors.New(fmt.Sprintf("no such language implementation passed to simulator on startup (%s)", langImpl))
				instr.TestFailure(err)
				continue
			}
			subfolder := fmt.Sprintf("%s-%s", langImpl, name)
			fullpath := filepath.Join(s.puppetDir, subfolder)
			p := Puppet{name: name, directory: fullpath, Port: s.acquirePort()}
			go startPuppet(s, p, langImpl)
			time.Sleep(1 * time.Second)
			feedID, err := DoWhoami(p)
			if err != nil {
				instr.TestFailure(err)
				continue
			}
			p.feedID = feedID
			s.puppetMap[name] = p
			instr.TestSuccess()
			taplog(fmt.Sprintf("%s has id %s", name, feedID))
			taplog(fmt.Sprintf("logging to %s.txt", name))
		case "log":
			srcPuppet := s.getSrcPuppet()
			amount, err := strconv.Atoi(instr.getSecond())
			if err != nil {
				log.Fatalln(err)
			}
			msg, err := DoLog(srcPuppet, amount)
			s.evaluateRun(err)
			taplog(msg)
		case "wait":
			ms, err := time.ParseDuration(fmt.Sprintf("%sms", instr.getFirst()))
			if err != nil {
				instr.TestFailure(err)
				continue
			}
			time.Sleep(ms)
			instr.TestSuccess()
		case "unfollow":
			fallthrough
		case "follow":
			srcPuppet := s.getSrcPuppet()
			dstPuppet := s.getDstPuppet()
			err := DoFollow(srcPuppet, dstPuppet, instr.command == "follow")
			s.evaluateRun(err)
		case "isfollowing":
			srcPuppet := s.getSrcPuppet()
			dstPuppet := s.getDstPuppet()
			err := DoIsFollowing(srcPuppet, dstPuppet)
			s.evaluateRun(err)
		case "isnotfollowing":
			srcPuppet := s.getSrcPuppet()
			dstPuppet := s.getDstPuppet()
			err := DoIsNotFollowing(srcPuppet, dstPuppet)
			s.evaluateRun(err)
		case "post":
			srcPuppet := s.getSrcPuppet()
			err := DoPost(srcPuppet)
			s.evaluateRun(err)
		case "disconnect":
			srcPuppet := s.getSrcPuppet()
			dstPuppet := s.getDstPuppet()
			err := DoDisconnect(srcPuppet, dstPuppet)
			s.evaluateRun(err)
		case "connect":
			srcPuppet := s.getSrcPuppet()
			dstPuppet := s.getDstPuppet()
			err := DoConnect(srcPuppet, dstPuppet)
			s.evaluateRun(err)
		case "has":
			arg := strings.Split(instr.getSecond(), "@")
			dst, seq := arg[0], arg[1]
			srcPuppet := s.getSrcPuppet()
			dstPuppet := s.puppetMap[dst]
			err := DoHast(srcPuppet, dstPuppet, seq)
			s.evaluateRun(err)
		default:
			instr.Print()
		}
	}
	fmt.Printf("1..%d\n", len(s.instructions))
}

func resetPuppetDir(dir string) {
	if dir == "/" || dir == "~" || dir == "C:/" {
		fmt.Println("you are trying to remove an important system folder, netsim will stop execution instead")
		os.Exit(0)
	}
	absdir, err := filepath.Abs(dir)
	if err != nil {
		log.Fatalln(err)
	}
	// remove the puppet dir and its subfolders
	err = os.RemoveAll(absdir)
	if err != nil {
		log.Fatalln(err)
	}
	// recreate it
	err = os.Mkdir(absdir, 0777)
	if err != nil {
		log.Fatalln(err)
	}
}

func main() {
	var testfile string
	flag.StringVar(&testfile, "spec", "./test.txt", "test file containing network simulator test instructions")
	var outdir string
	flag.StringVar(&outdir, "out", "./puppets", "the output directory containing instantiated netsim peers")
	var basePort int
	flag.IntVar(&basePort, "port", 18888, "start of port range used for each running sbot")
	flag.Parse()
	/*
	 * the language implementation dir contains the code for starting a puppet, via a shim.
	 * the puppet lives in another directory, which contains its secret.
	 *
	 * the language implementation needs to be passed:
	 *   the directory of the puppet's secret
	 *   the ports it will use
	 *
	 * the puppet directory needs to be created, and a secret needs to be instantiated for it.
	 * requirements:
	 *   an output directory containing all puppet folders
	 *   some way to instantiate seeded secrets for each puppet
	 */
	resetPuppetDir(outdir)
	sim := makeSimulator(basePort, outdir, flag.Args())
	lines := readTest(testfile)
	sim.ParseTest(lines)
	sim.execute()
}
