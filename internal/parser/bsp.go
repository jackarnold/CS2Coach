package parser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/NublyBR/go-vpk"
	"github.com/golang/geo/r3"
)

// BSPLoader handles loading BSP files from various sources
type BSPLoader struct {
	cs2Path  string
	tempDir  string
	mapsPath string
}

type Vector3 struct {
	X, Y, Z float32
}

type BSPData struct {
	Header     Header
	Lumps      []Lump
	VerticesXY []Vector3
	Planes     []Plane
	Nodes      []Node
	Leaves     []Leaf
}

type Header struct {
	Ident       [4]byte // Should be "VBSP"
	Version     int32
	MapRevision int32
	LumpCount   int32
}

type Lump struct {
	Offset       int32
	Length       int32
	Version      int32
	Uncompressed int32
}

type Plane struct {
	Normal   Vector3
	Distance float32
}

type Node struct {
	PlaneNum   int32
	Children   [2]int32 // negative numbers are leaf indices
	Mins, Maxs [3]int16
	FirstFace  uint16
	NumFaces   uint16
	Area       int16
	Padding    int16
}

type Leaf struct {
	Contents        int32
	Cluster         int16
	Area            int16
	Mins, Maxs      [3]int16
	FirstLeafFace   uint16
	NumLeafFaces    uint16
	FirstLeafBrush  uint16
	NumLeafBrushes  uint16
	LeafWaterDataID int16
}

type BSPVisibilityChecker struct {
	bspData        *BSPData
	visibilityData []byte
	worldData      []byte
	physicsData    []byte
}

// Lump IDs for Source 2 BSP format
const (
	LUMP_ENTITIES  = 0
	LUMP_PLANES    = 1
	LUMP_TEXDATA   = 2
	LUMP_VERTEXES  = 3
	LUMP_NODES     = 5
	LUMP_FACES     = 7
	LUMP_LEAVES    = 10
	LUMP_EDGES     = 12
	LUMP_SURFEDGES = 13
)

const defaultCS2Path = `C:\Program Files (x86)\Steam\steamapps\common\Counter-Strike Global Offensive\game\csgo`

// NewBSPLoader creates a new BSPLoader with the given CS2 installation path
func NewBSPLoader(cs2Path string) *BSPLoader {
	return &BSPLoader{
		cs2Path:  cs2Path,
		mapsPath: filepath.Join(cs2Path, "maps"),
		tempDir:  filepath.Join(os.TempDir(), "cs2coach_bsp"),
	}
}

func (b *BSPVisibilityChecker) LoadSource2MapFiles(mapDir string) error {
	vvisPath := filepath.Join(mapDir, "world_visibility.vvis_c")
	vwrldPath := filepath.Join(mapDir, "world.vwrld_c")
	vphysPath := filepath.Join(mapDir, "world_physics.vphys_c")

	fmt.Printf("Loading visibility file: %s\n", vvisPath)

	// Check and load visibility data
	if err := b.LoadVVISData(vvisPath); err != nil {
		return fmt.Errorf("failed to load visibility data: %v", err)
	}

	// Optionally load additional files for advanced features
	if _, err := os.Stat(vwrldPath); err == nil {
		b.worldData, _ = os.ReadFile(vwrldPath)
	}
	if _, err := os.Stat(vphysPath); err == nil {
		b.physicsData, _ = os.ReadFile(vphysPath)
	}

	return nil
}

func (b *BSPVisibilityChecker) LoadVVISData(vvisPath string) error {
	// Check if the file exists
	if _, err := os.Stat(vvisPath); os.IsNotExist(err) {
		return fmt.Errorf("visibility file not found: %s", vvisPath)
	}

	// Attempt to read the file
	data, err := os.ReadFile(vvisPath)
	if err != nil {
		return fmt.Errorf("failed to read visibility data from %s: %v", vvisPath, err)
	}

	// Validate the data (e.g., size or format)
	if len(data) < 1 {
		return fmt.Errorf("visibility data in %s is empty or invalid", vvisPath)
	}

	// Parse visibility data (adjust parsing logic for Source 2 as needed)
	b.visibilityData = data

	// Debug log (optional)
	fmt.Printf("[DEBUG] Successfully loaded visibility data from: %s\n", vvisPath)

	return nil
}

// LoadBSPForMap updated to work with Source 2 map files
// LoadBSPForMap updated to ensure correct paths for Source 2 map files
func (l *BSPLoader) LoadBSPForMap(mapName string) (*BSPVisibilityChecker, error) {
	fmt.Printf("Attempting to load BSP for map %s\n", mapName)
	fmt.Printf("CS2 Path: %s\n", l.cs2Path)
	fmt.Printf("Maps Path: %s\n", l.mapsPath)

	if !fileExists(l.cs2Path) {
		return nil, fmt.Errorf("CS2 path not found: %s", l.cs2Path)
	}

	// Create a temp directory for extracted map files
	mapExtractDir := filepath.Join(l.tempDir, "maps", mapName)
	if err := os.MkdirAll(mapExtractDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %v", err)
	}

	// Try extracting from map-specific VPK
	mapVPKPath := filepath.Join(l.cs2Path, "maps", mapName+".vpk")
	if fileExists(mapVPKPath) {
		if err := l.processVPKFile(mapVPKPath, mapName, mapExtractDir); err == nil {
			// Successfully found and extracted map files
			checker := &BSPVisibilityChecker{}
			if err := checker.LoadSource2MapFiles(mapExtractDir); err != nil {
				return nil, fmt.Errorf("failed to load Source 2 map files: %v", err)
			}
			return checker, nil
		}
	}

	// Try extracting from `pak01_dir.vpk` and its siblings
	files, err := os.ReadDir(l.cs2Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read CS2 directory: %v", err)
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(strings.ToLower(file.Name()), "pak01_") &&
			strings.HasSuffix(strings.ToLower(file.Name()), ".vpk") {
			vpkPath := filepath.Join(l.cs2Path, file.Name())
			if err := l.processVPKFile(vpkPath, mapName, mapExtractDir); err == nil {
				// Successfully found and extracted map files
				checker := &BSPVisibilityChecker{}
				if err := checker.LoadSource2MapFiles(mapExtractDir); err != nil {
					return nil, fmt.Errorf("failed to load Source 2 map files: %v", err)
				}
				return checker, nil
			}
		}
	}

	return nil, fmt.Errorf("map files not found in any VPK file")
}

// Update NewBSPVisibilityChecker to use the loader
func NewBSPVisibilityChecker(mapName string) (*BSPVisibilityChecker, error) {
	loader := NewBSPLoader(defaultCS2Path)
	return loader.LoadBSPForMap(mapName)
}

func loadBSPFromFile(bspPath string) (*BSPVisibilityChecker, error) {
	f, err := os.Open(bspPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	bspData, err := LoadBSP(f)
	if err != nil {
		return nil, err
	}

	return &BSPVisibilityChecker{bspData: bspData}, nil
}

func (l *BSPLoader) LoadBSPFromSpecificVPK(vpkPath, mapName string) (*BSPVisibilityChecker, error) {
	if !fileExists(vpkPath) {
		return nil, fmt.Errorf("VPK file not found: %s", vpkPath)
	}

	err := extractBSPFromVPK(vpkPath, mapName, l.tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to extract BSP from %s: %v", vpkPath, err)
	}

	return loadBSPFromFile(filepath.Join(l.tempDir, mapName+".bsp"))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (l *BSPLoader) ExtractBSPFromPath(path, mapName, outputDir string, recursive bool) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to access path: %v", err)
	}

	if fileInfo.IsDir() {
		return l.processDirectory(path, mapName, outputDir, recursive)
	}
	return l.processVPKFile(path, mapName, outputDir)
}

func (l *BSPLoader) processDirectory(dirPath, mapName, outputDir string, recursive bool) error {
	// Try to open the directory as a VPK directory using pak01_dir.vpk as the base
	pak01Path := filepath.Join(dirPath, "pak01_dir.vpk")
	if fileExists(pak01Path) {
		pak, err := vpk.OpenDir(dirPath)
		if err == nil {
			defer pak.Close()
			fmt.Printf("Processing VPK directory using: %s\n", dirPath)
			if err := l.extractBSPFiles(pak, mapName, outputDir); err != nil {
				fmt.Printf("Error processing directory: %v\n", err)
			}
			return nil
		}
		fmt.Printf("Could not open directory as VPK: %v\n", err)
	}

	if !recursive {
		return fmt.Errorf("not a VPK directory and recursive flag not set")
	}

	return filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if pak, err := vpk.OpenDir(path); err == nil {
				defer pak.Close()
				return l.extractBSPFiles(pak, mapName, outputDir)
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".vpk") {
			return l.processVPKFile(path, mapName, outputDir)
		}
		return nil
	})
}

func (l *BSPLoader) processVPKFile(vpkPath, mapName, outputDir string) error {
	pak, err := vpk.OpenAny(vpkPath)
	if err != nil {
		return fmt.Errorf("failed to open VPK %s: %v", vpkPath, err)
	}
	defer pak.Close()

	requiredFiles := []string{
		fmt.Sprintf("maps/%s/world_visibility.vvis_c", mapName),
		fmt.Sprintf("maps/%s/world.vwrld_c", mapName),
		fmt.Sprintf("maps/%s/world_physics.vphys_c", mapName),
	}

	foundFiles := make(map[string]vpk.Entry)

	for _, entry := range pak.Entries() {
		entryPath := strings.ToLower(entry.Filename())
		for _, reqFile := range requiredFiles {
			if entryPath == strings.ToLower(reqFile) {
				foundFiles[reqFile] = entry
				fmt.Printf("Found required file: %s\n", entry.Filename())
			}
		}
	}

	// Extract all found files
	for _, entry := range foundFiles {
		if err := l.extractMapFile(entry, outputDir); err != nil {
			fmt.Printf("Warning: failed to extract %s: %v\n", entry.Filename(), err)
		}
	}

	// Check if we found all required files
	if len(foundFiles) >= 2 { // Need at least world and physics files
		return nil
	}

	return fmt.Errorf("Not all required map files found")
}

func (l *BSPLoader) extractMapFile(entry vpk.Entry, outputDir string) error {
	fullPath := filepath.Join(outputDir, entry.Filename())

	// Create all parent directories
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create directories: %v", err)
	}

	outFile, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outFile.Close()

	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("failed to open entry: %v", err)
	}
	defer reader.Close()

	written, err := io.Copy(outFile, reader)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	fmt.Printf("Extracted %s (%d bytes)\n", fullPath, written)
	return nil
}

func (l *BSPLoader) extractBSPFiles(pak vpk.VPK, mapName, outputDir string) error {
	fmt.Printf("Searching for BSP files in VPK entries...\n")
	mapPattern := strings.ToLower(mapName + ".bsp")

	for _, entry := range pak.Entries() {
		fmt.Printf("Examining entry: %s\n", entry.Filename())
		if !strings.HasSuffix(strings.ToLower(entry.Filename()), ".bsp") {
			continue
		}

		if mapName != "" && !strings.Contains(strings.ToLower(entry.Filename()), mapPattern) {
			fmt.Printf("Skipping non-matching BSP: %s\n", entry.Filename())
			continue
		}

		if !entry.FilenameSafeWindows() && !entry.FilenameSafeUnix() {
			fmt.Printf("Skipping unsafe filename: %s\n", entry.Filename())
			continue
		}

		if err := l.extractBSPFile(entry, outputDir); err != nil {
			fmt.Printf("Error extracting %s: %v\n", entry.Filename(), err)
		}
	}
	return nil
}

func (l *BSPLoader) extractBSPFile(entry vpk.Entry, outputDir string) error {
	fmt.Printf("Found BSP: %s (size: %d bytes, CRC: %x) in Path: %s\n",
		entry.Filename(), entry.Length(), entry.CRC(), entry.Path())

	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("failed to open entry: %v", err)
	}
	defer reader.Close()

	outputFile := filepath.Join(outputDir, filepath.Base(entry.Filename()))
	outFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer outFile.Close()

	written, err := io.Copy(outFile, reader)
	if err != nil {
		return fmt.Errorf("failed to extract BSP: %v", err)
	}

	fmt.Printf("Extracted BSP to: %s (%d bytes)\n", outputFile, written)
	return nil
}

func extractBSPFromVPK(vpkPath, mapName, outputDir string) error {
	pak, err := vpk.OpenAny(vpkPath)
	if err != nil {
		return fmt.Errorf("failed to open VPK: %v", err)
	}
	defer pak.Close()

	// Add more potential path patterns
	paths := []string{
		fmt.Sprintf("maps/%s.bsp", mapName),
		fmt.Sprintf("maps/bsp/%s.bsp", mapName),
		fmt.Sprintf("maps/%s/%s.bsp", mapName, mapName),
		strings.ToLower(fmt.Sprintf("maps/%s.bsp", mapName)),
	}

	var foundEntry vpk.Entry
	for _, entry := range pak.Entries() {
		entryPath := strings.ToLower(entry.Filename())
		for _, path := range paths {
			if strings.EqualFold(entryPath, strings.ToLower(path)) {
				foundEntry = entry
				break
			}
		}
		if foundEntry != nil {
			break
		}
	}

	if foundEntry == nil {
		return fmt.Errorf("map BSP not found in VPK (searched paths: %v)", paths)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	outFile, err := os.Create(filepath.Join(outputDir, mapName+".bsp"))
	if err != nil {
		return err
	}
	defer outFile.Close()

	reader, err := foundEntry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()

	written, err := io.Copy(outFile, reader)
	if err != nil {
		return err
	}

	fmt.Printf("Wrote %d bytes to %s\n", written, outFile.Name())
	return nil
}

func (b *BSPVisibilityChecker) IsVisible(from, to r3.Vector) bool {
	// Convert to Vector3 for BSP functions
	start := Vector3{float32(from.X), float32(from.Y), float32(from.Z)}
	end := Vector3{float32(to.X), float32(to.Y), float32(to.Z)}

	direction := to.Sub(from)
	distance := direction.Norm()

	// Debug line
	fmt.Printf("[DEBUG] IsVisible() from=(%.1f, %.1f, %.1f) to=(%.1f, %.1f, %.1f), dist=%.1f\n",
		from.X, from.Y, from.Z,
		to.X, to.Y, to.Z,
		distance)

	// Simple cutoff for large distances
	if distance > 2000 {
		fmt.Println("[DEBUG] IsVisible(): distance > 2000, returning false")
		return false
	}

	// Check if either point is in a solid leaf
	startLeaf := b.bspData.findLeaf(start, 0)
	endLeaf := b.bspData.findLeaf(end, 0)

	if startLeaf.Contents&1 != 0 || endLeaf.Contents&1 != 0 {
		fmt.Println("[DEBUG] IsVisible(): start or end in solid leaf, returning false")
		return false
	}

	// Trace line through BSP tree
	result := b.bspData.CheckLineOfSight(start, end)
	fmt.Printf("[DEBUG] IsVisible(): final line-of-sight result = %v\n", result)
	return result
}

func readLumpData(r io.Reader, offset int64, size int) ([]byte, error) {
	// Skip to offset
	if n, err := io.CopyN(io.Discard, r, offset); err != nil || n != offset {
		return nil, errors.New("failed to reach offset")
	}
	// Read data
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func LoadBSP(r io.Reader) (*BSPData, error) {
	bsp := &BSPData{}

	if err := binary.Read(r, binary.LittleEndian, &bsp.Header); err != nil {
		return nil, err
	}

	if string(bsp.Header.Ident[:]) != "VBSP" {
		return nil, errors.New("invalid BSP file identifier")
	}

	// Read lumps
	bsp.Lumps = make([]Lump, bsp.Header.LumpCount)
	if err := binary.Read(r, binary.LittleEndian, bsp.Lumps); err != nil {
		return nil, err
	}

	// Load required data
	if err := bsp.loadVertices(r); err != nil {
		return nil, err
	}
	if err := bsp.loadPlanes(r); err != nil {
		return nil, err
	}
	if err := bsp.loadNodes(r); err != nil {
		return nil, err
	}
	if err := bsp.loadLeaves(r); err != nil {
		return nil, err
	}

	return bsp, nil
}

func (bsp *BSPData) loadVertices(r io.Reader) error {
	lump := bsp.Lumps[LUMP_VERTEXES]
	if lump.Length == 0 {
		return errors.New("vertex lump is empty")
	}

	data, err := readLumpData(r, int64(lump.Offset), int(lump.Length))
	if err != nil {
		return err
	}

	vertexCount := lump.Length / 12 // Each vertex is 12 bytes (3 * float32)
	bsp.VerticesXY = make([]Vector3, vertexCount)

	return binary.Read(bytes.NewReader(data), binary.LittleEndian, &bsp.VerticesXY)
}

func (bsp *BSPData) loadPlanes(r io.Reader) error {
	lump := bsp.Lumps[LUMP_PLANES]
	if lump.Length == 0 {
		return errors.New("plane lump is empty")
	}

	data, err := readLumpData(r, int64(lump.Offset), int(lump.Length))
	if err != nil {
		return err
	}

	planeCount := lump.Length / 20 // Each plane is 20 bytes (Vector3 normal + float32 distance)
	bsp.Planes = make([]Plane, planeCount)

	return binary.Read(bytes.NewReader(data), binary.LittleEndian, &bsp.Planes)
}

func (bsp *BSPData) loadNodes(r io.Reader) error {
	lump := bsp.Lumps[LUMP_NODES]
	if lump.Length == 0 {
		return errors.New("node lump is empty")
	}

	data, err := readLumpData(r, int64(lump.Offset), int(lump.Length))
	if err != nil {
		return err
	}

	nodeCount := lump.Length / 32 // Size of Node struct
	bsp.Nodes = make([]Node, nodeCount)

	return binary.Read(bytes.NewReader(data), binary.LittleEndian, &bsp.Nodes)
}

func (bsp *BSPData) loadLeaves(r io.Reader) error {
	lump := bsp.Lumps[LUMP_LEAVES]
	if lump.Length == 0 {
		return errors.New("leaf lump is empty")
	}

	data, err := readLumpData(r, int64(lump.Offset), int(lump.Length))
	if err != nil {
		return err
	}

	leafCount := lump.Length / 32 // Size of Leaf struct
	bsp.Leaves = make([]Leaf, leafCount)

	return binary.Read(bytes.NewReader(data), binary.LittleEndian, &bsp.Leaves)
}

func (bsp *BSPData) CheckLineOfSight(start, end Vector3) bool {
	// First check if points are in solid leaves
	startLeaf := bsp.findLeaf(start, 0)
	endLeaf := bsp.findLeaf(end, 0)

	if startLeaf.Contents&1 != 0 || endLeaf.Contents&1 != 0 {
		return false
	}

	// Then traverse the BSP tree to check visibility
	return bsp.traverseNode(0, start, end, 0, 1)
}

func (bsp *BSPData) findLeaf(point Vector3, nodeIndex int32) *Leaf {
	if nodeIndex < 0 {
		return &bsp.Leaves[^nodeIndex]
	}

	node := bsp.Nodes[nodeIndex]
	plane := bsp.Planes[node.PlaneNum]

	dist := dotProduct(plane.Normal, point) - plane.Distance

	if dist >= 0 {
		return bsp.findLeaf(point, node.Children[0])
	}
	return bsp.findLeaf(point, node.Children[1])
}

func (bsp *BSPData) traverseNode(nodeIndex int32, start, end Vector3, startFrac, endFrac float32) bool {
	if nodeIndex < 0 {
		leaf := bsp.Leaves[^nodeIndex]
		return leaf.Contents&1 == 0 // Not solid
	}

	node := bsp.Nodes[nodeIndex]
	plane := bsp.Planes[node.PlaneNum]

	startDist := dotProduct(plane.Normal, start) - plane.Distance
	endDist := dotProduct(plane.Normal, end) - plane.Distance

	const EPSILON = 0.03125

	// Check if line is entirely on one side
	if startDist >= EPSILON && endDist >= EPSILON {
		return bsp.traverseNode(node.Children[0], start, end, startFrac, endFrac)
	}
	if startDist < -EPSILON && endDist < -EPSILON {
		return bsp.traverseNode(node.Children[1], start, end, startFrac, endFrac)
	}

	// Line spans the splitting plane
	var side int32
	var frac float32
	if startDist < endDist {
		side = 1
		frac = startDist / (startDist - endDist)
	} else {
		side = 0
		frac = endDist / (endDist - startDist)
	}

	// Clamp to prevent precision issues
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}

	// Calculate intersection point
	mid := Vector3{
		X: start.X + frac*(end.X-start.X),
		Y: start.Y + frac*(end.Y-start.Y),
		Z: start.Z + frac*(end.Z-start.Z),
	}

	// Check near side
	if !bsp.traverseNode(node.Children[side], start, mid, startFrac, startFrac+(endFrac-startFrac)*frac) {
		return false
	}

	// Check far side
	return bsp.traverseNode(node.Children[1-side], mid, end, startFrac+(endFrac-startFrac)*frac, endFrac)
}

func dotProduct(a, b Vector3) float32 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}
