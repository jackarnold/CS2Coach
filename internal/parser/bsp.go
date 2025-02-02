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
	"sync"

	"github.com/NublyBR/go-vpk"
	"github.com/golang/geo/r3"
)

// BSPLoader handles loading BSP files from various sources
type BSPLoader struct {
	cs2Path    string
	tempDir    string
	mapsPath   string
	modelsPath string
	logger     slog.Logger
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
	Ident       [4]byte // Should be "VBSP" for maps and "VMDL" for models
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
	materialData   map[int]string
	materialCache  MaterialCache
	playerModel    *PlayerModel
}

type ModelHitbox struct {
	Mins   Vector3
	Maxs   Vector3
	Group  int32
	NameID int32
}

type PlayerModel struct {
	Hitboxes        []ModelHitbox
	StandingHeight  float64
	CrouchingHeight float64
	Width           float64
	HitboxNames     map[int32]string
}

func (pm *PlayerModel) String() string {
	var b strings.Builder
	b.WriteString("PlayerModel:\n")
	b.WriteString(fmt.Sprintf("  Heights: Standing=%.2f, Crouching=%.2f\n", pm.StandingHeight, pm.CrouchingHeight))
	b.WriteString(fmt.Sprintf("  Width: %.2f\n", pm.Width))
	b.WriteString("  Hitboxes:\n")

	for i, hb := range pm.Hitboxes {
		name := pm.HitboxNames[hb.Group]
		b.WriteString(fmt.Sprintf("    [%d] %s (Group=%d):\n", i, name, hb.Group))
		b.WriteString(fmt.Sprintf("      Mins: {X: %.2f, Y: %.2f, Z: %.2f}\n", hb.Mins.X, hb.Mins.Y, hb.Mins.Z))
		b.WriteString(fmt.Sprintf("      Maxs: {X: %.2f, Y: %.2f, Z: %.2f}\n", hb.Maxs.X, hb.Maxs.Y, hb.Maxs.Z))
	}

	return b.String()
}

// Material penetration properties
type MaterialProperties struct {
	penetrationModifier float32
	isWallbangable      bool
}

var materialPenetration = map[string]MaterialProperties{
	"DEFAULT":     {penetrationModifier: 0.0, isWallbangable: false},
	"GLASS":       {penetrationModifier: 0.8, isWallbangable: true},
	"WOOD":        {penetrationModifier: 0.6, isWallbangable: true},
	"METAL":       {penetrationModifier: 0.3, isWallbangable: true},
	"VENT":        {penetrationModifier: 0.5, isWallbangable: true},
	"GRATE":       {penetrationModifier: 0.8, isWallbangable: true},
	"THIN_METAL":  {penetrationModifier: 0.4, isWallbangable: true},
	"CONCRETE":    {penetrationModifier: 0.2, isWallbangable: true},
	"BRICK":       {penetrationModifier: 0.25, isWallbangable: true},
	"CHAIN_FENCE": {penetrationModifier: 0.9, isWallbangable: true},
}

type MaterialCache struct {
	pointToMaterial map[string]string
	mutex           sync.RWMutex
}

func NewMaterialCache() *MaterialCache {
	return &MaterialCache{
		pointToMaterial: make(map[string]string),
	}
}

func (mc *MaterialCache) getCachedMaterial(point Vector3) (string, bool) {
	key := fmt.Sprintf("%.0f,%.0f,%.0f", point.X, point.Y, point.Z)
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	mat, exists := mc.pointToMaterial[key]
	return mat, exists
}

func (mc *MaterialCache) cacheMaterial(point Vector3, material string) {
	key := fmt.Sprintf("%.0f,%.0f,%.0f", point.X, point.Y, point.Z)
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.pointToMaterial[key] = material
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

// NewBSPLoader creates a new BSPLoader with the given CS2 installation path
func NewBSPLoader(cs2Path string, logger slog.Logger) *BSPLoader {
	return &BSPLoader{
		cs2Path:    cs2Path,
		mapsPath:   filepath.Join(cs2Path, "maps"),
		modelsPath: filepath.Join(cs2Path, "models"),
		tempDir:    filepath.Join(os.TempDir(), "cs2coach_bsp"),
		logger:     logger,
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

func (b *BSPVisibilityChecker) LoadPlayerModel() error {
	modelData, err := os.ReadFile(`C:\Users\richa\go\src\github.com\richardkiene\CS2Coach\internal\parser\ctm_sas_model.txt`)
	if err != nil {
		return fmt.Errorf("failed to load model data: %v", err)
	}

	model, err := b.parseKV3ModelData(modelData)
	if err != nil {
		return fmt.Errorf("failed to parse model data: %v", err)
	}

	b.playerModel = model
	return nil
}

func (b *BSPVisibilityChecker) parseKV3ModelData(data []byte) (*PlayerModel, error) {
	parser := NewKV3Parser(string(data))
	parsed, err := parser.Parse()
	if err != nil {
		return nil, fmt.Errorf("failed to parse KV3 model data: %v", err)
	}

	modelData, ok := parsed.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid KV3 model data format")
	}

	hitboxSets, ok := modelData["m_hitboxsets"].([]interface{})
	if !ok || len(hitboxSets) == 0 {
		return nil, fmt.Errorf("no hitbox sets found")
	}

	var csHitboxSet map[string]interface{}
	for _, set := range hitboxSets {
		setMap, ok := set.(map[string]interface{})
		if !ok {
			continue
		}
		if key, ok := setMap["key"].(string); ok && key == "cstrike" {
			if value, ok := setMap["value"].(map[string]interface{}); ok {
				csHitboxSet = value
				break
			}
		}
	}

	if csHitboxSet == nil {
		return nil, fmt.Errorf("cstrike hitbox set not found")
	}

	hitboxes, ok := csHitboxSet["m_HitBoxes"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("no hitboxes found")
	}

	model := &PlayerModel{
		StandingHeight:  72.0,
		CrouchingHeight: 54.0,
		Width:           32.0,
		HitboxNames:     make(map[int32]string),
		Hitboxes:        make([]ModelHitbox, 0, len(hitboxes)),
	}

	for _, hb := range hitboxes {
		hitbox, ok := hb.(map[string]interface{})
		if !ok {
			continue
		}

		name := hitbox["m_name"].(string)
		var group int32
		value, ok := hitbox["m_nGroupId"]
		if !ok {
			// Handle the missing key error
			panic("m_nGroupId not found in hitbox")
		}

		if v, ok := value.(float64); ok {
			group = int32(v)
		} else if v, ok := value.(int); ok {
			group = int32(v)
		} else {
			panic(fmt.Sprintf("unexpected type %T for m_nGroupId", value))
		}

		minBounds, ok := hitbox["m_vMinBounds"].([]interface{})
		maxBounds, ok2 := hitbox["m_vMaxBounds"].([]interface{})
		if !ok || !ok2 || len(minBounds) != 3 || len(maxBounds) != 3 {
			continue
		}

		modelHitbox := ModelHitbox{
			Mins: Vector3{
				X: float32(minBounds[0].(float64)),
				Y: float32(minBounds[1].(float64)),
				Z: float32(minBounds[2].(float64)),
			},
			Maxs: Vector3{
				X: float32(maxBounds[0].(float64)),
				Y: float32(maxBounds[1].(float64)),
				Z: float32(maxBounds[2].(float64)),
			},
			Group:  group,
			NameID: group,
		}

		model.Hitboxes = append(model.Hitboxes, modelHitbox)
		model.HitboxNames[group] = name
	}

	if len(model.Hitboxes) == 0 {
		return nil, fmt.Errorf("no valid hitboxes found in model data")
	} else {
		b.logger.Debug("loaded KV3 PlayerModel", "model", model)
	}

	return model, nil
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

	return fmt.Errorf("not all required map files found")
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

func (b *BSPVisibilityChecker) IsVisible(player PlayerTickData, targetPlayer PlayerTickData) bool {
	from := player.Position
	to := targetPlayer.Position

	playerForward := player.ForwardVector()
	direction := to.Sub(from)
	distance := direction.Norm()
	if distance > 2000 {
		return false
	}

	directionToTarget := direction.Normalize()
	playerViewDirection := playerForward.Normalize()

	// FOV checks
	horizontalDot := r3.Vector{X: directionToTarget.X, Y: directionToTarget.Y, Z: 0}.Normalize().
		Dot(r3.Vector{X: playerViewDirection.X, Y: playerViewDirection.Y, Z: 0}.Normalize())
	horizontalFOVThreshold := math.Cos(90 * (math.Pi / 180))

	verticalDot := math.Abs(directionToTarget.Z)
	verticalFOVThreshold := math.Sin(74 * (math.Pi / 180))

	if horizontalDot < horizontalFOVThreshold || verticalDot > verticalFOVThreshold {
		return false
	}

	if b.bspData == nil || b.playerModel == nil {
		return false
	}

	// Get the appropriate hitboxes based on stance
	var hitboxesToCheck []ModelHitbox
	if targetPlayer.IsCrouched {
		hitboxesToCheck = b.getAdjustedHitboxes(b.playerModel.Hitboxes, true)
	} else {
		hitboxesToCheck = b.playerModel.Hitboxes
	}

	// Calculate elevation difference between players
	elevationDiff := to.Z - from.Z
	heightAdjustment := 0.0
	if math.Abs(elevationDiff) > b.playerModel.StandingHeight {
		heightAdjustment = math.Atan2(elevationDiff, math.Sqrt(direction.X*direction.X+direction.Y*direction.Y))
	}

	// Ray spread angles for narrow passages
	spreadAngles := []float64{-5, 0, 5} // degrees
	start := Vector3{float32(from.X), float32(from.Y), float32(from.Z)}

	right := playerForward.Cross(r3.Vector{X: 0, Y: 0, Z: 1}).Normalize()
	up := right.Cross(playerForward).Normalize()

	// Adjust base angles for elevation
	baseVerticalSpread := []float64{
		-5 + heightAdjustment*180/math.Pi,
		heightAdjustment * 180 / math.Pi,
		5 + heightAdjustment*180/math.Pi,
	}

	// Check visibility for each hitbox point with ray spreading
	for _, hitbox := range hitboxesToCheck {
		checkPoints := b.generateHitboxCheckPoints(hitbox, to)

		for _, point := range checkPoints {
			// Base direction to this hitbox point
			baseDirection := point.Sub(from)

			for _, horizontalSpread := range spreadAngles {
				for _, verticalSpread := range baseVerticalSpread {
					spreadRad := horizontalSpread * (math.Pi / 180)
					verticalRad := verticalSpread * (math.Pi / 180)

					rotatedDir := baseDirection
					rotatedDir = rotateVector(rotatedDir, right, verticalRad)
					rotatedDir = rotateVector(rotatedDir, up, spreadRad)

					// Add intermediate points for long distances with elevation
					if distance > 500 && math.Abs(elevationDiff) > b.playerModel.StandingHeight {
						midPoint := from.Add(rotatedDir.Mul(0.5))
						midEnd := Vector3{float32(midPoint.X), float32(midPoint.Y), float32(midPoint.Z)}
						if blocked, _ := b.bspData.hasVisualBlocker(start, midEnd, b); !blocked {
							continue
						}
					}

					spreadTarget := from.Add(rotatedDir)
					end := Vector3{float32(spreadTarget.X), float32(spreadTarget.Y), float32(spreadTarget.Z)}

					blocked, penetration := b.bspData.hasVisualBlocker(start, end, b)
					if !blocked || penetration > 0.3 {
						return true
					}
				}
			}
		}
	}

	return false
}

func (b *BSPVisibilityChecker) getAdjustedHitboxes(hitboxes []ModelHitbox, crouching bool) []ModelHitbox {
	adjusted := make([]ModelHitbox, len(hitboxes))
	copy(adjusted, hitboxes)

	if crouching {
		crouchScale := b.playerModel.CrouchingHeight / b.playerModel.StandingHeight
		for i := range adjusted {
			// Scale vertical positions for crouching
			adjusted[i].Mins.Z *= float32(crouchScale)
			adjusted[i].Maxs.Z *= float32(crouchScale)
		}
	}

	return adjusted
}

func (b *BSPVisibilityChecker) generateHitboxCheckPoints(hitbox ModelHitbox, basePos r3.Vector) []r3.Vector {
	// Generate key points to check for visibility
	points := []r3.Vector{
		// Center
		{
			X: basePos.X + float64((hitbox.Mins.X+hitbox.Maxs.X)/2),
			Y: basePos.Y + float64((hitbox.Mins.Y+hitbox.Maxs.Y)/2),
			Z: basePos.Z + float64((hitbox.Mins.Z+hitbox.Maxs.Z)/2),
		},
		// Top center
		{
			X: basePos.X + float64((hitbox.Mins.X+hitbox.Maxs.X)/2),
			Y: basePos.Y + float64((hitbox.Mins.Y+hitbox.Maxs.Y)/2),
			Z: basePos.Z + float64(hitbox.Maxs.Z),
		},
		// Bottom center
		{
			X: basePos.X + float64((hitbox.Mins.X+hitbox.Maxs.X)/2),
			Y: basePos.Y + float64((hitbox.Mins.Y+hitbox.Maxs.Y)/2),
			Z: basePos.Z + float64(hitbox.Mins.Z),
		},
	}

	// Add corner points for more precise checking
	corners := []r3.Vector{
		{X: basePos.X + float64(hitbox.Mins.X), Y: basePos.Y + float64(hitbox.Mins.Y), Z: basePos.Z + float64(hitbox.Mins.Z)},
		{X: basePos.X + float64(hitbox.Maxs.X), Y: basePos.Y + float64(hitbox.Mins.Y), Z: basePos.Z + float64(hitbox.Mins.Z)},
		{X: basePos.X + float64(hitbox.Mins.X), Y: basePos.Y + float64(hitbox.Maxs.Y), Z: basePos.Z + float64(hitbox.Mins.Z)},
		{X: basePos.X + float64(hitbox.Maxs.X), Y: basePos.Y + float64(hitbox.Maxs.Y), Z: basePos.Z + float64(hitbox.Mins.Z)},
		{X: basePos.X + float64(hitbox.Mins.X), Y: basePos.Y + float64(hitbox.Mins.Y), Z: basePos.Z + float64(hitbox.Maxs.Z)},
		{X: basePos.X + float64(hitbox.Maxs.X), Y: basePos.Y + float64(hitbox.Mins.Y), Z: basePos.Z + float64(hitbox.Maxs.Z)},
		{X: basePos.X + float64(hitbox.Mins.X), Y: basePos.Y + float64(hitbox.Maxs.Y), Z: basePos.Z + float64(hitbox.Maxs.Z)},
		{X: basePos.X + float64(hitbox.Maxs.X), Y: basePos.Y + float64(hitbox.Maxs.Y), Z: basePos.Z + float64(hitbox.Maxs.Z)},
	}

	points = append(points, corners...)
	return points
}

func rotateVector(v, axis r3.Vector, angle float64) r3.Vector {
	cos := math.Cos(angle)
	sin := math.Sin(angle)

	// Rodrigues rotation formula
	return v.Mul(cos).Add(
		axis.Cross(v).Mul(sin)).Add(
		axis.Mul(axis.Dot(v) * (1 - cos)))
}

// hasVisualBlocker checks if there are any solid nodes between two points
func (bsp *BSPData) hasVisualBlocker(start, end Vector3, checker *BSPVisibilityChecker) (bool, float32) {
	maxSteps := 32
	steps := 0
	maxPenetration := float32(0.0)
	hasSolid := false

	points := []Vector3{
		start,
		{(start.X + end.X) / 2, (start.Y + end.Y) / 2, (start.Z + end.Z) / 2},
		end,
	}

	distance := math.Sqrt(float64(
		(end.X-start.X)*(end.X-start.X) +
			(end.Y-start.Y)*(end.Y-start.Y) +
			(end.Z-start.Z)*(end.Z-start.Z)))

	// Reduce checks for distant targets
	if distance > 1000 {
		points = []Vector3{start, end}
	}

	for _, point := range points {
		node := int32(0)
		solid := false

		for steps < maxSteps {
			steps++

			if node < 0 {
				leafIndex := ^node
				if leafIndex >= int32(len(bsp.Leaves)) {
					return true, 0
				}
				leaf := bsp.Leaves[leafIndex]
				solid = leaf.Contents&1 != 0
				break
			}

			if node >= int32(len(bsp.Nodes)) {
				return true, 0
			}

			current := bsp.Nodes[node]
			if current.PlaneNum >= int32(len(bsp.Planes)) {
				return true, 0
			}

			plane := bsp.Planes[current.PlaneNum]
			dist := dotProduct(plane.Normal, point) - plane.Distance

			if solid && !hasSolid {
				hasSolid = true
				// Only check materials when we hit something solid
				if mat, exists := checker.materialCache.getCachedMaterial(point); exists {
					if props, ok := materialPenetration[mat]; ok && props.penetrationModifier > maxPenetration {
						maxPenetration = props.penetrationModifier
					}
				} else {
					material := checker.getMaterialAtPoint(point)
					checker.materialCache.cacheMaterial(point, material)
					if props, ok := materialPenetration[material]; ok && props.penetrationModifier > maxPenetration {
						maxPenetration = props.penetrationModifier
					}
				}
			}

			if dist >= 0 {
				node = current.Children[0]
			} else {
				node = current.Children[1]
			}
		}

		if solid && maxPenetration < 0.3 {
			return true, maxPenetration
		}
	}

	return false, maxPenetration
}

func (b *BSPVisibilityChecker) parseMaterialData() error {
	if b.worldData == nil {
		return fmt.Errorf("world data not loaded")
	}

	// Parse world.vwrld_c for material references
	// Format: VWLD header followed by material entries
	reader := bytes.NewReader(b.worldData)

	// Skip VWLD header (typically 16 bytes)
	if _, err := reader.Seek(16, io.SeekStart); err != nil {
		return fmt.Errorf("failed to skip header: %v", err)
	}

	b.materialData = make(map[int]string)

	// Read material entries
	// Each entry: 4 bytes index + variable length material name
	for {
		var index int32
		err := binary.Read(reader, binary.LittleEndian, &index)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read material index: %v", err)
		}

		// Read material name length (1 byte)
		var nameLen uint8
		if err := binary.Read(reader, binary.LittleEndian, &nameLen); err != nil {
			return fmt.Errorf("failed to read name length: %v", err)
		}

		// Read material name
		nameBuf := make([]byte, nameLen)
		if _, err := reader.Read(nameBuf); err != nil {
			return fmt.Errorf("failed to read material name: %v", err)
		}

		matName := string(nameBuf)
		b.materialData[int(index)] = matName
	}

	return nil
}

func (b *BSPVisibilityChecker) getMaterialAtPoint(point Vector3) string {
	if b.materialData == nil {
		if err := b.parseMaterialData(); err != nil {
			b.logger.Error("Failed to parse material data", "error", err)
			return "DEFAULT"
		}
	}

	// Find closest surface to point
	var closestDist float32 = math.MaxFloat32
	var materialIndex int = -1

	// Traverse BSP tree to find closest surface
	node := int32(0)
	for node >= 0 && node < int32(len(b.bspData.Nodes)) {
		current := b.bspData.Nodes[node]
		if current.PlaneNum >= int32(len(b.bspData.Planes)) {
			break
		}

		plane := b.bspData.Planes[current.PlaneNum]
		dist := dotProduct(plane.Normal, point) - plane.Distance

		if dist < closestDist {
			closestDist = dist
			materialIndex = int(current.FirstFace)
		}

		if dist >= 0 {
			node = current.Children[0]
		} else {
			node = current.Children[1]
		}
	}

	if materialIndex != -1 {
		if material, exists := b.materialData[materialIndex]; exists {
			// Map material name to penetration type
			switch {
			case strings.Contains(material, "glass"):
				return "GLASS"
			case strings.Contains(material, "wood"):
				return "WOOD"
			case strings.Contains(material, "metal"):
				return "METAL"
			case strings.Contains(material, "vent"):
				return "VENT"
			case strings.Contains(material, "grate"):
				return "GRATE"
			case strings.Contains(material, "concrete"):
				return "CONCRETE"
			case strings.Contains(material, "brick"):
				return "BRICK"
			case strings.Contains(material, "fence"):
				return "CHAIN_FENCE"
			default:
				return "DEFAULT"
			}
		}
	}

	return "DEFAULT"
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
