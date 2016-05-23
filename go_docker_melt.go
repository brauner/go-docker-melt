package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/brauner/go-docker-melt/tarutils"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
)

type genericConfig struct {
	Hostname     string   `json:"Hostname,omitempty"`
	Domainname   string   `json:"Domainname,omitempty"`
	User         string   `json:"User,omitempty"`
	AttachStdin  bool     `json:"AttachStdin,omitempty"`
	AttachStdout bool     `json:"AttachStdout,omitempty"`
	AttachStderr bool     `json:"AttachStderr,omitempty"`
	Tty          bool     `json:"Tty,omitempty"`
	OpenStdin    bool     `json:"OpenStdin,omitempty"`
	StdinOnce    bool     `json:"StdinOnce,omitempty"`
	Env          []string `json:"Env,omitempty"`
	Cmd          []string `json:"Cmd,omitempty"`
	Image        string   `json:"Image,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	Entrypoint   []string `json:"Entrypoint,omitempty"`
	OnBuild      []string `json:"OnBuild,omitempty"`
	rawJSON      []byte
}

// https://gist.github.com/aaronlehmann/b42a2eaf633fc949f93b
type History struct {
	Created    string `json:"created,omitempty"`
	Author     string `json:"author,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	Comment    string `json:"comment,omitempty"`
	EmptyLayer bool   `json:"empty_layer,omitempty"`
}

// https://gist.github.com/aaronlehmann/b42a2eaf633fc949f93b
type Rootfs struct {
	Type    string   `json:"type,omitempty"`
	DiffIds []string `json:"diff_ids,omitempty"`
}

func (rfs *Rootfs) delRootfsElem(pos int) {
	rfs.DiffIds = append(rfs.DiffIds[:pos], rfs.DiffIds[pos+1:]...)
}

type ImageConfig struct {
	Arch            string           `json:"architecture,omitempty"`
	Config          *genericConfig   `json:"config,omitempty"`
	Container       string           `json:"container,omitempty"`
	ContainerConfig *genericConfig   `json:"container_config,omitempty"`
	Created         string           `json:"created,omitempty"`
	DockerVersion   string           `json:"docker_version,omitempty"`
	RawHistory      *json.RawMessage `json:"history,omitempty"`
	history         []History
	OS              string           `json:"os,omitempty"`
	RawRootfs       *json.RawMessage `json:"rootfs,omitempty"`
	rootfs          *Rootfs
	rawJSON         []byte
}

func (img *ImageConfig) UnmarshalJSON(file string) error {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_EXCL, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	size := fi.Size()
	if !(size > 0) {
		return nil
	}

	buf := make([]byte, size)
	_, err = f.Read(buf)
	if err != nil {
		return err
	}

	err = json.Unmarshal(buf, &img)
	if err != nil {
		return err
	}
	img.rawJSON = buf

	if img.RawHistory == nil || img.RawRootfs == nil {
		return errors.New("Corrupt image configuration.")
	}

	err = json.Unmarshal(*img.RawHistory, &img.history)
	if err != nil {
		return err
	}

	err = json.Unmarshal(*img.RawRootfs, &img.rootfs)
	if err != nil {
		return err
	}

	if img.rootfs == nil {
		return errors.New("Corrupt image configuration.")
	}

	return nil
}

func (img *ImageConfig) updateHistory() error {
	repl, err := json.Marshal(img.history)
	if err != nil {
		return err
	}
	img.rawJSON = bytes.Replace(img.rawJSON, *img.RawHistory, repl, 1)
	return nil
}

func (img *ImageConfig) updateRootfs() error {
	repl, err := json.Marshal(img.rootfs)
	if err != nil {
		return err
	}
	img.rawJSON = bytes.Replace(img.rawJSON, *img.RawRootfs, repl, 1)
	return nil
}

func (img *ImageConfig) delHistoryElem(pos int) {
	img.history = append(img.history[:pos], img.history[pos+1:]...)
}

// The reference for manifests can be found at:
// https://github.com/docker/distribution/blob/master/docs/spec/manifest-v2-2.md
// However, we do not need to support this currently since docker save only
// exports in the format outlined in this struct.
type Manifest struct {
	ConfigHash string `json:"Config,omitempty"`
	config     *ImageConfig
	RepoTags   []string `json:"RepoTags,omitempty"`
	layers     []string
	RawLayers  *json.RawMessage `json:"Layers,omitempty"`
	Parent     string
}

func (m *Manifest) delLayerElem(pos int) {
	m.layers = append(m.layers[:pos], m.layers[pos+1:]...)
}

type RawManifest struct {
	Manifest []Manifest
	rawJSON  []byte // holds raw manifest.json file
}

func (r *RawManifest) updateLayers(manifest Manifest) error {
	repl, err := json.Marshal(manifest.layers)
	if err != nil {
		return err
	}
	r.rawJSON = bytes.Replace(r.rawJSON, *manifest.RawLayers, repl, 1)
	return nil
}

func (r *RawManifest) UnmarshalJSON(file string) error {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_EXCL, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	size := fi.Size()
	if !(size > 0) {
		return nil
	}

	buf := make([]byte, size)
	_, err = f.Read(buf)
	if err != nil {
		return err
	}

	err = json.Unmarshal(buf, &r.Manifest)
	if err != nil {
		return err
	}

	for i := 0; i < len(r.Manifest); i++ {
		manfst := &r.Manifest[i]
		if manfst.RawLayers == nil {
			return errors.New("Corrupt manifest file.")
		}
		err = json.Unmarshal(*manfst.RawLayers, &manfst.layers)
		if err != nil {
			return err
		}
	}
	r.rawJSON = buf
	return nil
}

// Currently unused since we currently do not support squashing of v1 images
// that do not rely on manifest.json.
type LayerJSON struct {
	Id              string         `json:"id,omitempty"`
	Parent          string         `json:"parent,omitempty"`
	Created         string         `json:"created,omitempty"`
	Container       string         `json:"container,omitempty"`
	ContainerConfig *genericConfig `json:"container_config,omitempty"`
	DockerVersion   string         `json:"docker_version,omitempty"`
	Config          *genericConfig `json:"config,omitempty"`
	Arch            string         `json:"architecture,omitempty"`
	OS              string         `json:"os,omitempty"`
	rawJSON         []byte
}

// TODO: Should be replaced by a go-only implementation (cf. the functions for
// tar archive creation in the Tar interface in tarutils/tarutils.go).
func untarCmd(from string, to string) *exec.Cmd {
	cmd := exec.Command("tar", "--acls", "--xattrs", "--xattrs-include=*",
		"--same-owner", "--numeric-owner",
		"--preserve-permissions", "--atime-preserve=system",
		"-S", "-xf", from, "-C", to)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func rsyncLayer(from string, to string) *exec.Cmd {
	fromexcl := from + "/./"
	cmd := exec.Command("rsync", "-aXhsrpR", "--numeric-ids",
		"--remove-source-files", "--exclude=.wh.*", fromexcl, to)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func isWhiteout(name string) (bool, error) {
	regex, err := regexp.Compile(`^\.wh\.[[:alnum:]]+`)
	if err != nil {
		return false, err
	}
	if regex.MatchString(name) {
		return true, nil
	}
	return false, nil
}

// This implements a barebone recursive readdir() since the filepath.Walk()
// function causes unnecessary overhead due to it sorting the directory entries.
func removeWhiteouts(oldpath string, newpath string, nentries int) error {
	f, err := os.Open(oldpath)
	if err != nil {
		return err
	}
	defer f.Close()

	var dirEntries = make([]os.FileInfo, nentries)
	var cur string
	for dirEntries, err = f.Readdir(nentries); err != io.EOF && err == nil; dirEntries, err = f.Readdir(nentries) {
		for _, n := range dirEntries {
			cur = n.Name()
			curTmp := filepath.Join(oldpath, cur)
			newTmp := filepath.Join(newpath, cur)
			if n.IsDir() {
				removeWhiteouts(curTmp, newTmp, nentries)
			} else {
				wh, err := isWhiteout(cur)
				if err != nil {
					return err
				}
				if wh {
					if err := os.RemoveAll(filepath.Join(newpath, cur[ /* .wh. */ 4:])); err != nil {
						return err
					}
				}
			}
		}
	}
	return err
}

func IsEmptyDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	return err
}

var image string
var imageOut string
var tmpDir string

func init() {
	flag.StringVar(&image, "i", "", "Tarball of the image to melt.")
	flag.StringVar(&imageOut, "o", "", "Name of output tarball.")
	flag.StringVar(&tmpDir, "t", "", "Directory to hold temporary data.")
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Parse()
	if image == "" || imageOut == "" {
		Usage()
		os.Exit(1)
	}

	log.SetFlags(log.Lshortfile)

	tmpDir, err := ioutil.TempDir(tmpDir, "go-docker-melt_")
	if err != nil {
		log.Fatal(err)
	}

	// TODO: Should be replaced by a go-only implementation (cf. the functions for
	// tar archive creation in the Tar interface in tarutils/tarutils.go).
	untar := untarCmd(image, tmpDir)
	err = untar.Run()
	if err != nil {
		log.Fatal(err)
	}

	var manifest RawManifest
	err = manifest.UnmarshalJSON(filepath.Join(tmpDir, "manifest.json"))
	if err != nil {
		log.Fatal(err)
	}

	numManifest := len(manifest.Manifest)
	var numLayers int
	var configs = make([]ImageConfig, numManifest, numManifest)
	for i, val := range manifest.Manifest {
		numLayers += len(val.layers)
		conf := val.ConfigHash
		if conf == "" {
			continue
		}
		err = configs[i].UnmarshalJSON(filepath.Join(tmpDir, conf))
		if err != nil {
			log.Fatal(err)
		}
		manifest.Manifest[i].config = &configs[i]
	}

	// Check if it is worth doing any work at all.
	if numLayers <= 1 {
		fmt.Errorf("%s\n", "Image does only have one layer.")
		fmt.Errorf("%s\n", "There is nothing to be done.")
		os.Exit(0)
	}

	// Maybe we can make the hashmap already in the preceding loop to avoid
	// looping through all of this again.
	// Let m be the runtime of the outer loop, n the runtime of the inner
	// loop. Then adding all keys has complexity O(m*n).
	// The allLayers hashmap holds all layers for all images in the tar
	// archive without duplicates. If the int it indicates is set to 1 the
	// layer is shared at least among two layers. If it is set to 0 the
	// layer is unique.
	allLayers := make(map[string]int, numLayers)
	for _, val := range manifest.Manifest {
		for _, lay := range val.layers {
			if ret, ok := allLayers[lay]; !ok {
				allLayers[lay] = 0 // unique layer
			} else if ret == 0 { // only set it when it isn't already set
				allLayers[lay]++ // shared layer
			}
		}
	}

	// The next checks only make sense when we found multiple config objects
	// in the manifest.json file. Otherwise this is pointless work.
	if numManifest > 1 {
		var uniqueLayers int
		for _, val := range allLayers {
			if val == 0 {
				uniqueLayers++
			}
		}
		if uniqueLayers == 0 {
			fmt.Errorf("%s\n", "All layers are shared among images.")
			fmt.Errorf("%s\n", "There is nothing to be done.")
			os.Exit(0)
		}
		var cur, prev string
		// If the preceeding layer "prev" is shared and followed by a
		// unique layer "cur" we cannot melt "cur" into "prev". To
		// indicate this we assign the value 2.
		for _, val := range manifest.Manifest {
			for i := 1; i < len(val.layers); i++ {
				cur = val.layers[i]
				prev = val.layers[i-1]
				if (allLayers[cur] == 0) && (allLayers[prev] == 1) {
					allLayers[prev]++
				}
			}
		}
	}

	// The untaring can be parallelized.
	var wg sync.WaitGroup
	maxWorkers := runtime.NumCPU()
	tasks := make(chan *exec.Cmd, numLayers)
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			for cmd := range tasks {
				err = cmd.Run()
				if err != nil {
					log.Println(err)
				}
			}
			wg.Done()
		}()
	}

	for key := range allLayers {
		// We need to record the pure layerHash somewhere to avoid
		// duplicating the work. That's for future tweaking.
		layerHash := key[:len(key)- /* /layer.tar */ 10]
		direntries, err := ioutil.ReadDir(filepath.Join(tmpDir, layerHash))
		if err != nil {
			os.Exit(1)
		}
		// There usually are only a few (<=3) entries per directory so
		// there's no point in using goroutines for this.
		for _, val := range direntries {
			curName := val.Name()
			if curName == "layer.tar" {
				continue
			}
			err = os.Remove(filepath.Join(tmpDir, layerHash, curName))
			if err != nil {
				log.Println(err)
			}
		}
		// Unpacking everything under sha-hash/layer
		tmptar := key[:len(key)- /* .tar */ 4]
		err = os.Mkdir(filepath.Join(tmpDir, tmptar), 0755)
		if err != nil {
			log.Fatal(err)
		}
		tasks <- untarCmd(filepath.Join(tmpDir, key), filepath.Join(tmpDir, tmptar))
	}
	close(tasks)
	wg.Wait()

	// sync + delete witheouts
	var rootLayer string
	for i := 0; i < len(manifest.Manifest); i++ {
		manfst := &manifest.Manifest[i]
		if manfst.config == nil {
			log.Fatalln("Corrupt image configuration file.")
		}

		rootLayer = ""
		for j := 0; j < len(manfst.layers); j++ {
			layer := &manfst.layers[j]
			// Find the first useable rootLayer for this image.
			if rootLayer == "" && allLayers[*layer] != 2 {
				rootLayer = (*layer)[:len(*layer)- /* .tar */ 4]
				continue
			}

			// This layer will be melted into the current chosen
			// rootLayer.
			layerHash := (*layer)[:len(*layer)- /* .tar */ 4]
			meltFrom := filepath.Join(tmpDir, layerHash)
			meltInto := filepath.Join(tmpDir, rootLayer)

			// melt
			_, err := os.Stat(meltFrom)
			if err == nil {
				// rsync everything except whiteout files.
				cmd := rsyncLayer(meltFrom, meltInto)
				// log.Println(meltFrom, meltInto)
				err = cmd.Run()
				if err != nil {
					log.Fatal(err)
				}
				// Delete whiteout files in the current layer
				// and the corresponding file/dir in the
				// rootLayer.
				err = removeWhiteouts(meltFrom, meltInto, 20)
				if err != io.EOF {
					log.Fatal(err)
				}
				// Delete melted layers.
				err := os.RemoveAll(filepath.Join(tmpDir, layerHash[:len(layerHash)- /* /layer */ 6]))
				if err != nil {
					log.Fatal(err)
				}
			}

			// The next layer cannot be melted into the current
			// rootLayer.
			if allLayers[*layer] == 2 {
				rootLayer = ""
			}

			// Delete corresponding history entry for this layer.
			manfst.config.delHistoryElem(j)
			// Delete corresponding diff_ids entry for this layer.
			manfst.config.rootfs.delRootfsElem(j)
			// Delete corresponding layer entry.
			manfst.delLayerElem(j)
			j--
		}
		err = manfst.config.updateHistory()
		if err != nil {
			log.Fatal(err)
		}

		err = manifest.updateLayers(*manfst)
		if err != nil {
			log.Fatal(err)
		}
	}
	err = ioutil.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifest.rawJSON, 0666)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: Rethink whether usage of a diffID map can be avoided.
	var diffIDMutex = struct {
		sync.Mutex
		diffID map[string]string
	}{diffID: make(map[string]string, len(allLayers))}
	sem := make(chan bool, maxWorkers)
	for key := range allLayers {
		l := filepath.Join(tmpDir, key)
		_, err = os.Stat(l)
		if os.IsNotExist(err) {
			continue
		}

		err = os.Remove(l)
		if err != nil {
			log.Fatal(err)
		}

		dir := filepath.Join(tmpDir, key[:len(key)- /* .tar */ 4])
		sem <- true
		go func(l string, dir string, key string) {
			defer func() { <-sem }()
			checksum, err := tarutils.CreateTarHash(l, dir, dir)
			if err != nil {
				log.Fatal(err)
			}
			diffIDMutex.Lock()
			diffIDMutex.diffID[key] = "sha256:" + hex.EncodeToString(checksum)
			diffIDMutex.Unlock()
			err = os.RemoveAll(dir)
			if err != nil {
				log.Fatal(err)
			}
		}(l, dir, key)
	}

	for i := 0; i < cap(sem); i++ {
		sem <- true
	}
	close(sem)

	for i := 0; i < len(manifest.Manifest); i++ {
		m := &manifest.Manifest[i]
		for j := 0; j < len(m.layers); j++ {
			l := &m.layers[j]
			m.config.rootfs.DiffIds[j] = diffIDMutex.diffID[*l]
		}
		err = m.config.updateRootfs()
		if err != nil {
			log.Fatal(err)
		}
		err = ioutil.WriteFile(filepath.Join(tmpDir, m.ConfigHash), m.config.rawJSON, 0666)
		if err != nil {
			log.Fatal(err)
		}
	}

	err = tarutils.CreateTar(imageOut, tmpDir, tmpDir)
	if err != nil {
		log.Fatal(err)
	}

	err = os.RemoveAll(tmpDir)
	if err != nil {
		log.Println(err)
	}
}
