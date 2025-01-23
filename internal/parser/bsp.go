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
	bspData *BSPData
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

// LoadBSPForMap attempts to load the BSP for the given map name, trying multiple sources
func (l *BSPLoader) LoadBSPForMap(mapName string) (*BSPVisibilityChecker, error) {
	// Check if CS2 path exists
	if !fileExists(l.cs2Path) {
		return nil, fmt.Errorf("CS2 path not found: %s", l.cs2Path)
	}

	// Create temp directory if needed
	if err := os.MkdirAll(l.tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %v", err)
	}

	// Try both potential VPK locations
	vpkPaths := []string{
		filepath.Join(l.cs2Path, "pak01_dir.vpk"),
		filepath.Join(l.cs2Path, "maps"),
	}

	for _, vpkPath := range vpkPaths {
		err := extractBSPFromVPK(vpkPath, mapName, l.tempDir)
		if err == nil {
			return loadBSPFromFile(filepath.Join(l.tempDir, mapName+".bsp"))
		}
	}

	return nil, fmt.Errorf("failed to extract BSP from any VPK location")
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

func extractBSPFromVPK(vpkPath, mapName, outputDir string) error {
	vpkPath = strings.TrimRight(vpkPath, "\\/")

	fmt.Printf("Attempting to extract %s from %s\n", mapName, vpkPath)
	pak, err := vpk.OpenAny(vpkPath)
	if err != nil {
		return fmt.Errorf("failed to open VPK: %v", err)
	}
	defer pak.Close()

	paths := []string{
		fmt.Sprintf("maps\\%s.bsp", mapName),
		fmt.Sprintf("maps\\bsp\\%s.bsp", mapName),
	}

	var foundEntry vpk.Entry
	for _, entry := range pak.Entries() {
		fmt.Printf("Found entry: %s\n", entry.Filename())
		for _, path := range paths {
			if strings.EqualFold(entry.Filename(), path) {
				foundEntry = entry
				break
			}
		}
		if foundEntry != nil {
			break
		}
	}

	if foundEntry == nil {
		return fmt.Errorf("map BSP not found in VPK")
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
	start := Vector3{
		X: float32(from.X),
		Y: float32(from.Y),
		Z: float32(from.Z),
	}

	end := Vector3{
		X: float32(to.X),
		Y: float32(to.Y),
		Z: float32(to.Z),
	}

	return b.bspData.CheckLineOfSight(start, end)
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
