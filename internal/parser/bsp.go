package parser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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
	logger   slog.Logger
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
	logger         slog.Logger
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
func NewBSPLoader(cs2Path string, logger slog.Logger) *BSPLoader {
	return &BSPLoader{
		cs2Path:  cs2Path,
		mapsPath: filepath.Join(cs2Path, "maps"),
		tempDir:  filepath.Join(os.TempDir(), "cs2coach_bsp"),
		logger:   logger,
	}
}

func (b *BSPVisibilityChecker) LoadSource2MapFiles(mapDir string) error {
	vvisPath := filepath.Join(mapDir, "world_visibility.vvis_c")
	vwrldPath := filepath.Join(mapDir, "world.vwrld_c")
	vphysPath := filepath.Join(mapDir, "world_physics.vphys_c")

	// Initialize BSP data structure for Source 2
	b.bspData = &BSPData{
		Header: Header{
			Ident:     [4]byte{'V', 'B', 'S', 'P'},
			Version:   1,
			LumpCount: 64,
		},
		Nodes:  make([]Node, 4096), // Reasonable size for CS2 maps
		Planes: make([]Plane, 4096),
		Leaves: make([]Leaf, 4096),
	}

	// Load visibility data
	if err := b.LoadVVISData(vvisPath); err != nil {
		return fmt.Errorf("failed to load visibility data: %v", err)
	}

	// Load world data
	if worldData, err := os.ReadFile(vwrldPath); err == nil {
		b.worldData = worldData
	}

	// Load physics data
	if physData, err := os.ReadFile(vphysPath); err == nil {
		b.physicsData = physData
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
	b.logger.Debug("Successfully loaded visibility data", "vvisPath", vvisPath)

	return nil
}

// LoadBSPForMap updated to work with Source 2 map files
// LoadBSPForMap updated to ensure correct paths for Source 2 map files
func (l *BSPLoader) LoadBSPForMap(mapName string) (*BSPVisibilityChecker, error) {
	l.logger.Debug("Attempting to load BSP Data", "Map Name", mapName, "CS2 Path", l.cs2Path, "Maps Path", l.mapsPath)

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
			checker.logger = l.logger
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
func NewBSPVisibilityChecker(mapName string, cs2MapsPath string, logger slog.Logger) (*BSPVisibilityChecker, error) {
	loader := NewBSPLoader(cs2MapsPath, logger)
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

				l.logger.Info("Found required file",
					slog.String("filename", entry.Filename()),
				)
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

	l.logger.Debug("Map File Extracted", "fullPath", fullPath, "written", written)
	return nil
}

func (l *BSPLoader) extractBSPFiles(pak vpk.VPK, mapName, outputDir string) error {
	l.logger.Debug("Searching for vvis_c files in VPK entries")
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
	l.logger.Info("Found BSP",
		slog.String("filename", entry.Filename()),
		slog.Int("size_bytes", int(entry.Length())),
		slog.String("crc", fmt.Sprintf("%x", entry.CRC())),
		slog.String("path", entry.Path()),
	)

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

	l.logger.Info("Extracted BSP",
		slog.String("output_file", outputFile),
		slog.Int64("size_bytes", written), // Assuming `written` is an int
	)

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

// IsVisible determines if there's a clear line of sight between two points
func (b *BSPVisibilityChecker) IsVisible(from, to r3.Vector) bool {
	// Simple distance check first
	direction := to.Sub(from)
	distance := direction.Norm()
	if distance > 2000 {
		return false
	}

	// Early out if BSP data isn't loaded
	if b.bspData == nil {
		return false
	}

	// Convert to local coords
	start := Vector3{float32(from.X), float32(from.Y), float32(from.Z)}
	end := Vector3{float32(to.X), float32(to.Y), float32(to.Z)}

	// Check main visibility line
	return !b.bspData.hasVisualBlocker(start, end)
}

// hasVisualBlocker checks if there are any solid nodes between two points
func (bsp *BSPData) hasVisualBlocker(start, end Vector3) bool {
	maxSteps := 32
	steps := 0

	// Points in 3D space between start and end
	points := []Vector3{
		start,
		{(start.X + end.X) / 2, (start.Y + end.Y) / 2, (start.Z + end.Z) / 2},
		end,
	}

	// Check if any point along the line intersects with solid
	for _, point := range points {
		node := int32(0)
		solid := false

		// Walk down BSP tree until we hit a leaf
		for steps < maxSteps {
			steps++

			if node < 0 { // Leaf node
				leafIndex := ^node
				if leafIndex >= int32(len(bsp.Leaves)) {
					return true
				}
				leaf := bsp.Leaves[leafIndex]
				solid = leaf.Contents&1 != 0
				break
			}

			if node >= int32(len(bsp.Nodes)) {
				return true
			}

			current := bsp.Nodes[node]
			if current.PlaneNum >= int32(len(bsp.Planes)) {
				return true
			}

			plane := bsp.Planes[current.PlaneNum]
			dist := dotProduct(plane.Normal, point) - plane.Distance

			if dist >= 0 {
				node = current.Children[0]
			} else {
				node = current.Children[1]
			}
		}

		if solid {
			return true
		}
	}

	return false
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
	// Base case - reached max recursion depth
	maxDepth := int32(64) // Typical max BSP tree depth
	for depth := int32(0); depth < maxDepth; depth++ {
		if nodeIndex < 0 {
			// Convert to leaf index by flipping bits
			leafIndex := ^nodeIndex
			if leafIndex >= int32(len(bsp.Leaves)) {
				return nil
			}
			return &bsp.Leaves[leafIndex]
		}

		if nodeIndex >= int32(len(bsp.Nodes)) {
			return nil
		}

		node := bsp.Nodes[nodeIndex]
		if node.PlaneNum >= int32(len(bsp.Planes)) {
			return nil
		}

		plane := bsp.Planes[node.PlaneNum]
		dist := dotProduct(plane.Normal, point) - plane.Distance

		if dist >= 0 {
			nodeIndex = node.Children[0]
		} else {
			nodeIndex = node.Children[1]
		}
	}

	// Hit max depth - return first leaf as fallback
	if len(bsp.Leaves) > 0 {
		return &bsp.Leaves[0]
	}
	return nil
}

func (bsp *BSPData) traverseNode(nodeIndex int32, start, end Vector3, startFrac, endFrac float32) bool {
	depth := 0
	maxDepth := 32
	visited := make(map[int32]bool)

	var traverse func(nodeIndex int32, start, end Vector3, startFrac, endFrac float32) bool
	traverse = func(nodeIndex int32, start, end Vector3, startFrac, endFrac float32) bool {
		if depth >= maxDepth || visited[nodeIndex] {
			return true // Default visible after max depth or cycle detected
		}
		visited[nodeIndex] = true
		depth++

		if nodeIndex < 0 {
			leafIndex := ^nodeIndex
			if leafIndex >= int32(len(bsp.Leaves)) {
				return false
			}
			leaf := bsp.Leaves[leafIndex]
			return leaf.Contents&1 == 0 // Not solid
		}

		if nodeIndex >= int32(len(bsp.Nodes)) {
			return false
		}

		node := bsp.Nodes[nodeIndex]
		if node.PlaneNum >= int32(len(bsp.Planes)) {
			return false
		}

		plane := bsp.Planes[node.PlaneNum]
		startDist := dotProduct(plane.Normal, start) - plane.Distance
		endDist := dotProduct(plane.Normal, end) - plane.Distance

		const EPSILON = 0.03125

		if startDist >= EPSILON && endDist >= EPSILON {
			return traverse(node.Children[0], start, end, startFrac, endFrac)
		}
		if startDist < -EPSILON && endDist < -EPSILON {
			return traverse(node.Children[1], start, end, startFrac, endFrac)
		}

		var side int32
		var frac float32
		if startDist < endDist {
			side = 1
			frac = startDist / (startDist - endDist)
		} else {
			side = 0
			frac = endDist / (endDist - startDist)
		}

		frac = float32(math.Max(0, math.Min(1, float64(frac))))

		mid := Vector3{
			X: start.X + frac*(end.X-start.X),
			Y: start.Y + frac*(end.Y-start.Y),
			Z: start.Z + frac*(end.Z-start.Z),
		}

		if !traverse(node.Children[side], start, mid, startFrac, startFrac+(endFrac-startFrac)*frac) {
			return false
		}

		return traverse(node.Children[1-side], mid, end, startFrac+(endFrac-startFrac)*frac, endFrac)
	}

	return traverse(nodeIndex, start, end, startFrac, endFrac)
}

func dotProduct(a, b Vector3) float32 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}
