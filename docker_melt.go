package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"github.com/brauner/docker-go-melt/tarutils"
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
	rawJSON []byte
}

func (rfs *Rootfs) delRootfsElem(pos int) {
	rfs.DiffIds = append(rfs.DiffIds[:pos], rfs.DiffIds[pos+1:]...)
}

type ImageConfig struct {
	Arch            string         `json:"architecture,omitempty"`
	Config          *genericConfig `json:"config,omitempty"`
	Container       string         `json:"container,omitempty"`
	ContainerConfig *genericConfig `json:"container_config,omitempty"`
	Created         string         `json:"created,omitempty"`
	DockerVersion   string         `json:"docker_version,omitempty"`
	History         []History      `json:"history,omitempty"`
	OS              string         `json:"os,omitempty"`
	Rootfs          *Rootfs        `json:"rootfs,omitempty"`
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
	return nil
}

func (img *ImageConfig) updateHistory(oldHist []byte) error {
	repl, err := json.Marshal(img.History)
	if err != nil {
		return err
	}
	img.rawJSON = bytes.Replace(img.rawJSON, oldHist, repl, 1)
	return nil
}

func (img *ImageConfig) updateRootfs(oldRootfs []byte) error {
	repl, err := json.Marshal(img.Rootfs)
	if err != nil {
		return err
	}
	img.rawJSON = bytes.Replace(img.rawJSON, oldRootfs, repl, 1)
	return nil
}

func (img *ImageConfig) updateRawJSON(oldHist []byte, oldRootfs []byte) error {
	err := img.updateHistory(oldHist)
	if err != nil {
		return err
	}
	err = img.updateRootfs(oldRootfs)
	if err != nil {
		return err
	}
	return nil
}

func (img *ImageConfig) delHistoryElem(pos int) {
	img.History = append(img.History[:pos], img.History[pos+1:]...)
}

// The reference for manifests can be found at:
// https://github.com/docker/distribution/blob/master/docs/spec/manifest-v2-2.md
// However, we do not need to support this currently since docker save only
// exports in the format outlined in this struct.
type Manifest struct {
	ConfigHash string `json:"Config,omitempty"`
	config     *ImageConfig
	RepoTags   []string `json:"RepoTags,omitempty"`
	Layers     []string `json:"Layers,omitempty"`
	Parent     string
}

func (m *Manifest) delLayerElem(pos int) {
	m.Layers = append(m.Layers[:pos], m.Layers[pos+1:]...)
}

type RawManifest struct {
	Manifest []Manifest
	rawJSON  []byte // holds raw manifest.json file
}

func (r *RawManifest) updateLayers(manifest Manifest, oldLayers []byte) error {
	repl, err := json.Marshal(manifest.Layers)
	if err != nil {
		return err
	}
	r.rawJSON = bytes.Replace(r.rawJSON, oldLayers, repl, 1)
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
					if err := os.RemoveAll(filepath.Join(newpath, cur[/* .wh. */ 4:])); err != nil {
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
var tmpFolder string

func init() {
	flag.StringVar(&image, "i", "", "Tarball of the image to melt.")
	flag.StringVar(&imageOut, "o", "", "Name of output tarball.")
	flag.StringVar(&tmpFolder, "t", "", "Directory to hold temporary data.")
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Parse()
	if image == "" || tmpFolder == "" || imageOut == "" {
		Usage()
		os.Exit(1)
	}

	untar := untarCmd(image, tmpFolder)
	if err := untar.Run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	var manifest RawManifest
	if err := manifest.UnmarshalJSON(filepath.Join(tmpFolder, "manifest.json")); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	numManifest := len(manifest.Manifest)
	var numLayers int
	var configs = make([]ImageConfig, numManifest, numManifest)
	for i, val := range manifest.Manifest {
		numLayers += len(val.Layers)
		conf := val.ConfigHash
		if conf == "" {
			continue
		}
		if err := configs[i].UnmarshalJSON(filepath.Join(tmpFolder, conf)); err != nil {
			fmt.Println(err)
			os.Exit(1)
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
		for _, lay := range val.Layers {
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
			for i := 1; i < len(val.Layers); i++ {
				cur = val.Layers[i]
				prev = val.Layers[i-1]
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
				if err := cmd.Run(); err != nil {
					fmt.Println(err)
				}
			}
			wg.Done()
		}()
	}

	for key := range allLayers {
		// We need to record the pure layerHash somewhere to avoid
		// duplicating the work. That's for future tweaking.
		layerHash := key[:len(key)- /* /layer.tar */ 10]
		direntries, err := ioutil.ReadDir(filepath.Join(tmpFolder, layerHash))
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
			if err := os.Remove(filepath.Join(tmpFolder, layerHash, curName)); err != nil {
				fmt.Println(err)
			}
		}
		// Unpacking everything under sha-hash/layer
		tmptar := key[:len(key)- /* .tar */ 4]
		if err := os.Mkdir(filepath.Join(tmpFolder, tmptar), 0755); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		tasks <- untarCmd(filepath.Join(tmpFolder, key), filepath.Join(tmpFolder, tmptar))
	}
	close(tasks)
	wg.Wait()

	// sync + delete witheouts
	var rootLayer string
	for i := 0; i < len(manifest.Manifest); i++ {
		manfst := &manifest.Manifest[i]
		if manfst.config == nil {
			fmt.Println("Corrupt image configuration file.")
			os.Exit(1)
		}

		rootLayer = ""
		oldHist, err := json.Marshal(manfst.config.History)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		if manfst.config.Rootfs == nil {
			fmt.Println("Corrupt image configuration file.")
			os.Exit(1)
		}
		oldRootfs, err := json.Marshal(manfst.config.Rootfs)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		manfst.config.Rootfs.rawJSON = oldRootfs

		oldLayers, err := json.Marshal(manfst.Layers)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		for j := 0; j < len(manfst.Layers); j++ {
			layer := &manfst.Layers[j]
			// Find the first useable rootLayer for this image.
			if rootLayer == "" && allLayers[*layer] != 2 {
				rootLayer = (*layer)[:len(*layer)- /* .tar */ 4]
				continue
			}

			// This layer will be melted into the current chosen
			// rootLayer.
			layerHash := (*layer)[:len(*layer)- /* .tar */ 4]
			meltFrom := filepath.Join(tmpFolder, layerHash)
			meltInto := filepath.Join(tmpFolder, rootLayer)

			// melt
			if _, err := os.Stat(meltFrom); err == nil {
				// rsync everything except whiteout files.
				cmd := rsyncLayer(meltFrom, meltInto)
				fmt.Println(meltFrom, meltInto)
				if err := cmd.Run(); err != nil {
					fmt.Println(err)
					os.Exit(1)
				}
				// Delete whiteout files in the current layer
				// and the corresponding file/dir in the
				// rootLayer.
				if err := removeWhiteouts(meltFrom, meltInto, 20); err != io.EOF {
					fmt.Println(err)
					os.Exit(1)
				}
				// Delete melted layers.
				if err := os.RemoveAll(filepath.Join(tmpFolder, layerHash[:len(layerHash)- /* /layer */ 6])); err != nil {
					fmt.Println(err)
					os.Exit(1)
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
			manfst.config.Rootfs.delRootfsElem(j)
			// Delete corresponding layer entry.
			manfst.delLayerElem(j)
			j--
		}
		err = manfst.config.updateHistory(oldHist)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		err = manifest.updateLayers(*manfst, oldLayers)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	if err := ioutil.WriteFile(filepath.Join(tmpFolder, "manifest.json"), manifest.rawJSON, 0666); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// TODO: This is a fast and dirty hack to get a working version. This
	// should (a) be parallelized, (b) to rethought whether usage of a
	// diffID map can be avoided.
	var diffID = make(map[string]string, len(allLayers))
	for key := range allLayers {
		l := filepath.Join(tmpFolder, key)
		if _, err := os.Stat(l); os.IsNotExist(err) {
			continue
		}
		if err := os.Remove(l); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		layer := filepath.Join(tmpFolder, key[:len(key)- /* .tar */ 4])
		checksum, err := tarutils.CreateTarHash(layer, layer)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		diffID[key] = "sha256:" + hex.EncodeToString(checksum)
		// Remove untared layer folder.
		if err := os.RemoveAll(layer); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	for _, val := range manifest.Manifest {
		for i, lay := range val.Layers {
			val.config.Rootfs.DiffIds[i] = diffID[lay]
		}
		err := val.config.updateRootfs(val.config.Rootfs.rawJSON)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		marshConfig, err := json.Marshal(val.config)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		if err := ioutil.WriteFile(filepath.Join(tmpFolder, val.ConfigHash), marshConfig, 0666); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	err := tarutils.CreateTar(tmpFolder, tmpFolder)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
