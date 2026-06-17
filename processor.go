package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var appVersion = "1.1.0-gui"

type Options struct {
	CjpegliPath  string
	JpegtranPath string
	Quality      int
	Sampling     string
	MaxWidth     int
	TargetKB     int
	MinQuality   int
	MaxQuality   int
	OutDir       string
	Recursive    bool
	Overwrite    bool
	KeepIfLarger bool
	UseJpegtran  bool
	DryRun       bool
}

type Job struct{ Input, Root string }

type Result struct {
	Input, Output        string
	Original, Compressed int64
	Width, Height        int
	Quality              int
	Skipped              bool
	Error                error
	Message              string
}

type Summary struct {
	TotalIn, TotalOut   int64
	OK, Skipped, Failed int
	Duration            time.Duration
	OutRoot             string
}

func init() {
	image.RegisterFormat("jpeg", "\xff\xd8", jpeg.Decode, jpeg.DecodeConfig)
	image.RegisterFormat("png", "\x89PNG\r\n\x1a\n", png.Decode, png.DecodeConfig)
}

func normalizeOptions(opt *Options) {
	opt.Sampling = strings.TrimSpace(opt.Sampling)
	switch opt.Sampling {
	case "420", "422", "444":
	default:
		opt.Sampling = "420"
	}
	if opt.Quality < 1 {
		opt.Quality = 1
	}
	if opt.Quality > 100 {
		opt.Quality = 100
	}
	if opt.MinQuality < 1 {
		opt.MinQuality = 1
	}
	if opt.MaxQuality > 100 {
		opt.MaxQuality = 100
	}
	if opt.MinQuality > opt.MaxQuality {
		opt.MinQuality, opt.MaxQuality = opt.MaxQuality, opt.MinQuality
	}
}

func runBatch(inputs []string, opt Options, logf func(string)) Summary {
	start := time.Now()
	normalizeOptions(&opt)
	jobs, outRoot, err := collectJobs(inputs, opt)
	s := Summary{OutRoot: outRoot}
	if err != nil {
		logf("错误：" + err.Error())
		s.Failed++
		s.Duration = time.Since(start)
		return s
	}
	if len(jobs) == 0 {
		logf("没有找到可处理图片。支持 jpg/jpeg/png")
		s.Duration = time.Since(start)
		return s
	}
	logf(fmt.Sprintf("输入数量：%d", len(jobs)))
	logf("输出目录：" + outRoot)
	if opt.TargetKB > 0 {
		logf(fmt.Sprintf("参数：target=%dKB, min-q=%d, max-q=%d, sampling=%s, max-width=%d", opt.TargetKB, opt.MinQuality, opt.MaxQuality, opt.Sampling, opt.MaxWidth))
	} else {
		logf(fmt.Sprintf("参数：q=%d, sampling=%s, max-width=%d", opt.Quality, opt.Sampling, opt.MaxWidth))
	}
	logf(strings.Repeat("-", 60))
	for i, job := range jobs {
		res := processOne(job, outRoot, opt)
		if res.Original > 0 {
			s.TotalIn += res.Original
		}
		if res.Compressed > 0 {
			s.TotalOut += res.Compressed
		}
		if res.Error != nil {
			s.Failed++
			logf(fmt.Sprintf("[%d/%d] 失败：%s\r\n  %v", i+1, len(jobs), filepath.Base(res.Input), res.Error))
			continue
		}
		if res.Skipped {
			s.Skipped++
			logf(fmt.Sprintf("[%d/%d] 跳过：%s  %s", i+1, len(jobs), filepath.Base(res.Input), res.Message))
			continue
		}
		s.OK++
		ratio := 0.0
		if res.Original > 0 {
			ratio = 100 * (1 - float64(res.Compressed)/float64(res.Original))
		}
		logf(fmt.Sprintf("[%d/%d] OK：%s  %.1fKB -> %.1fKB  下降 %.1f%%  q=%d  %dx%d", i+1, len(jobs), filepath.Base(res.Input), kb(res.Original), kb(res.Compressed), ratio, res.Quality, res.Width, res.Height))
	}
	logf(strings.Repeat("-", 60))
	s.Duration = time.Since(start)
	ratio := 0.0
	if s.TotalIn > 0 && s.TotalOut > 0 {
		ratio = 100 * (1 - float64(s.TotalOut)/float64(s.TotalIn))
	}
	logf(fmt.Sprintf("完成：成功 %d，跳过 %d，失败 %d，用时 %s", s.OK, s.Skipped, s.Failed, s.Duration.Round(time.Millisecond)))
	if s.TotalIn > 0 && s.TotalOut > 0 {
		logf(fmt.Sprintf("总体：%.1fMB -> %.1fMB，下降 %.1f%%", mb(s.TotalIn), mb(s.TotalOut), ratio))
	}
	return s
}

func collectJobs(inputs []string, opt Options) ([]Job, string, error) {
	cleaned := make([]string, 0, len(inputs))
	for _, p := range inputs {
		p = strings.Trim(strings.TrimSpace(p), "\"")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return nil, "", errors.New("没有输入路径")
	}
	outRoot := opt.OutDir
	if outRoot == "" {
		if len(cleaned) == 1 {
			st, err := os.Stat(cleaned[0])
			if err == nil && st.IsDir() {
				outRoot = cleaned[0] + "_compressed"
			} else {
				dir := filepath.Dir(cleaned[0])
				name := strings.TrimSuffix(filepath.Base(cleaned[0]), filepath.Ext(cleaned[0]))
				outRoot = filepath.Join(dir, name+"_compressed")
			}
		} else {
			cwd, _ := os.Getwd()
			outRoot = filepath.Join(cwd, "compressed_output")
		}
	}
	outRoot, _ = filepath.Abs(outRoot)
	var jobs []Job
	for _, input := range cleaned {
		abs, err := filepath.Abs(input)
		if err != nil {
			return nil, outRoot, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, outRoot, err
		}
		if st.IsDir() {
			root := abs
			if opt.Recursive {
				filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return nil
					}
					if isSupported(path) {
						jobs = append(jobs, Job{path, root})
					}
					return nil
				})
			} else {
				entries, _ := os.ReadDir(abs)
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					p := filepath.Join(abs, e.Name())
					if isSupported(p) {
						jobs = append(jobs, Job{p, root})
					}
				}
			}
		} else if isSupported(abs) {
			jobs = append(jobs, Job{abs, filepath.Dir(abs)})
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Input < jobs[j].Input })
	return jobs, outRoot, nil
}

func processOne(job Job, outRoot string, opt Options) Result {
	res := Result{Input: job.Input}
	st, err := os.Stat(job.Input)
	if err != nil {
		res.Error = err
		return res
	}
	res.Original = st.Size()
	rel, err := filepath.Rel(job.Root, job.Input)
	if err != nil {
		rel = filepath.Base(job.Input)
	}
	rel = strings.TrimSuffix(rel, filepath.Ext(rel)) + ".jpg"
	outPath := filepath.Join(outRoot, rel)
	res.Output = outPath
	if !opt.Overwrite {
		if _, err := os.Stat(outPath); err == nil {
			res.Skipped = true
			res.Message = "输出已存在，勾选覆盖可覆盖"
			return res
		}
	}
	if opt.DryRun {
		res.Skipped = true
		res.Message = "dry-run"
		return res
	}
	imgBytes, err := os.ReadFile(job.Input)
	if err != nil {
		res.Error = err
		return res
	}
	orientation := 1
	if isJpeg(job.Input) {
		orientation = exifOrientation(imgBytes)
	}
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		res.Error = fmt.Errorf("图片解码失败：%w", err)
		return res
	}
	nrgba := toNRGBAWhite(img)
	nrgba = applyOrientation(nrgba, orientation)
	if opt.MaxWidth > 0 && nrgba.Bounds().Dx() > opt.MaxWidth {
		newW := opt.MaxWidth
		newH := int(math.Round(float64(nrgba.Bounds().Dy()) * float64(newW) / float64(nrgba.Bounds().Dx())))
		if newH < 1 {
			newH = 1
		}
		nrgba = resizeBilinear(nrgba, newW, newH)
	}
	res.Width, res.Height = nrgba.Bounds().Dx(), nrgba.Bounds().Dy()
	tempDir, err := os.MkdirTemp("", "tea-jpegli-*")
	if err != nil {
		res.Error = err
		return res
	}
	defer os.RemoveAll(tempDir)
	tempPNG := filepath.Join(tempDir, "input.png")
	f, err := os.Create(tempPNG)
	if err != nil {
		res.Error = err
		return res
	}
	if err := png.Encode(f, nrgba); err != nil {
		f.Close()
		res.Error = err
		return res
	}
	f.Close()
	finalTmp := filepath.Join(tempDir, "out.jpg")
	q := opt.Quality
	if opt.TargetKB > 0 {
		q, err = encodeTarget(tempPNG, finalTmp, opt)
	} else {
		err = runCjpegli(opt.CjpegliPath, tempPNG, finalTmp, opt.Quality, opt.Sampling)
	}
	if err != nil {
		res.Error = err
		return res
	}
	res.Quality = q
	if opt.UseJpegtran && opt.JpegtranPath != "" {
		jtOut := filepath.Join(tempDir, "out_jpegtran.jpg")
		if err := runJpegtran(opt.JpegtranPath, finalTmp, jtOut); err == nil {
			finalTmp = jtOut
		}
	}
	outInfo, err := os.Stat(finalTmp)
	if err != nil {
		res.Error = err
		return res
	}
	if opt.KeepIfLarger && isJpeg(job.Input) && outInfo.Size() >= res.Original {
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			res.Error = err
			return res
		}
		if err := copyFile(job.Input, outPath); err != nil {
			res.Error = err
			return res
		}
		res.Compressed = res.Original
		res.Skipped = true
		res.Message = "压缩后更大，已保留原 JPG"
		return res
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		res.Error = err
		return res
	}
	if err := copyFile(finalTmp, outPath); err != nil {
		res.Error = err
		return res
	}
	fi, _ := os.Stat(outPath)
	if fi != nil {
		res.Compressed = fi.Size()
	}
	return res
}

func encodeTarget(inputPNG, outputJPG string, opt Options) (int, error) {
	targetBytes := int64(opt.TargetKB) * 1024
	low, high := opt.MinQuality, opt.MaxQuality
	bestQ := opt.MinQuality
	bestPath := ""
	tempDir := filepath.Dir(outputJPG)
	for low <= high {
		mid := (low + high) / 2
		p := filepath.Join(tempDir, fmt.Sprintf("q_%d.jpg", mid))
		err := runCjpegli(opt.CjpegliPath, inputPNG, p, mid, opt.Sampling)
		if err != nil {
			return mid, err
		}
		st, err := os.Stat(p)
		if err != nil {
			return mid, err
		}
		if st.Size() <= targetBytes {
			bestQ = mid
			bestPath = p
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if bestPath == "" {
		bestQ = opt.MinQuality
		p := filepath.Join(tempDir, fmt.Sprintf("q_%d_min.jpg", bestQ))
		if err := runCjpegli(opt.CjpegliPath, inputPNG, p, bestQ, opt.Sampling); err != nil {
			return bestQ, err
		}
		bestPath = p
	}
	return bestQ, copyFile(bestPath, outputJPG)
}

func runCjpegli(cjpegli, input, output string, quality int, sampling string) error {
	args := []string{input, "-q", strconv.Itoa(quality), "--chroma_subsampling=" + sampling, output}
	cmd := exec.Command(cjpegli, args...)
	hideChildWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("cjpegli 执行失败：%s", msg)
	}
	return nil
}
func runJpegtran(jpegtran, input, output string) error {
	args := []string{"-copy", "none", "-optimize", "-progressive", "-outfile", output, input}
	cmd := exec.Command(jpegtran, args...)
	hideChildWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("jpegtran 执行失败：%s", msg)
	}
	return nil
}

func hideChildWindow(cmd *exec.Cmd) {
	// GUI 程序调用 cjpegli/jpegtran 这种控制台 exe 时，避免每张图弹出黑窗口。
	const CREATE_NO_WINDOW = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: CREATE_NO_WINDOW}
}

func toNRGBAWhite(img image.Image) *image.NRGBA {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if a == 0 {
				dst.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
				continue
			}
			aa := float64(a) / 65535.0
			rr := uint8(math.Round((float64(r)/257.0)*aa + 255*(1-aa)))
			gg := uint8(math.Round((float64(g)/257.0)*aa + 255*(1-aa)))
			bb := uint8(math.Round((float64(bl)/257.0)*aa + 255*(1-aa)))
			dst.SetNRGBA(x, y, color.NRGBA{rr, gg, bb, 255})
		}
	}
	return dst
}
func resizeBilinear(src *image.NRGBA, newW, newH int) *image.NRGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if newW == sw && newH == sh {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, newW, newH))
	xRatio := float64(sw-1) / float64(max(1, newW-1))
	yRatio := float64(sh-1) / float64(max(1, newH-1))
	for y := 0; y < newH; y++ {
		sy := yRatio * float64(y)
		y0 := int(math.Floor(sy))
		y1 := min(y0+1, sh-1)
		fy := sy - float64(y0)
		for x := 0; x < newW; x++ {
			sx := xRatio * float64(x)
			x0 := int(math.Floor(sx))
			x1 := min(x0+1, sw-1)
			fx := sx - float64(x0)
			c00 := src.NRGBAAt(x0, y0)
			c10 := src.NRGBAAt(x1, y0)
			c01 := src.NRGBAAt(x0, y1)
			c11 := src.NRGBAAt(x1, y1)
			dst.SetNRGBA(x, y, color.NRGBA{bilerp(c00.R, c10.R, c01.R, c11.R, fx, fy), bilerp(c00.G, c10.G, c01.G, c11.G, fx, fy), bilerp(c00.B, c10.B, c01.B, c11.B, fx, fy), 255})
		}
	}
	return dst
}
func bilerp(a, b, c, d uint8, fx, fy float64) uint8 {
	top := float64(a)*(1-fx) + float64(b)*fx
	bot := float64(c)*(1-fx) + float64(d)*fx
	return uint8(math.Round(top*(1-fy) + bot*fy))
}

func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 < len(data) {
		if data[i] != 0xFF {
			break
		}
		marker := data[i+1]
		i += 2
		if marker == 0xDA || marker == 0xD9 {
			break
		}
		if i+2 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[i : i+2]))
		if segLen < 2 || i+segLen > len(data) {
			break
		}
		seg := data[i+2 : i+segLen]
		if marker == 0xE1 && len(seg) > 14 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return parseTIFFOrientation(seg[6:])
		}
		i += segLen
	}
	return 1
}
func parseTIFFOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	if t[0] == 'I' && t[1] == 'I' {
		order = binary.LittleEndian
	} else if t[0] == 'M' && t[1] == 'M' {
		order = binary.BigEndian
	} else {
		return 1
	}
	if order.Uint16(t[2:4]) != 42 {
		return 1
	}
	off := int(order.Uint32(t[4:8]))
	if off < 0 || off+2 > len(t) {
		return 1
	}
	count := int(order.Uint16(t[off : off+2]))
	pos := off + 2
	for n := 0; n < count; n++ {
		if pos+12 > len(t) {
			return 1
		}
		tag := order.Uint16(t[pos : pos+2])
		typ := order.Uint16(t[pos+2 : pos+4])
		cnt := order.Uint32(t[pos+4 : pos+8])
		if tag == 0x0112 && typ == 3 && cnt >= 1 {
			val := order.Uint16(t[pos+8 : pos+10])
			if val >= 1 && val <= 8 {
				return int(val)
			}
			return 1
		}
		pos += 12
	}
	return 1
}
func applyOrientation(src *image.NRGBA, o int) *image.NRGBA {
	if o == 1 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	var dst *image.NRGBA
	switch o {
	case 2:
		dst = image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(w-1-x, y, src.NRGBAAt(x, y))
			}
		}
	case 3:
		dst = image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(w-1-x, h-1-y, src.NRGBAAt(x, y))
			}
		}
	case 4:
		dst = image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(x, h-1-y, src.NRGBAAt(x, y))
			}
		}
	case 5:
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(y, x, src.NRGBAAt(x, y))
			}
		}
	case 6:
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(h-1-y, x, src.NRGBAAt(x, y))
			}
		}
	case 7:
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(h-1-y, w-1-x, src.NRGBAAt(x, y))
			}
		}
	case 8:
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(y, w-1-x, src.NRGBAAt(x, y))
			}
		}
	default:
		return src
	}
	return dst
}

func isSupported(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}
func isJpeg(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".jpg" || ext == ".jpeg"
}
func findTool(name, exeDir string) string {
	candidates := []string{filepath.Join(exeDir, "tools", name), filepath.Join(exeDir, name)}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if strings.HasSuffix(strings.ToLower(name), ".exe") {
		base := strings.TrimSuffix(name, ".exe")
		if p, err := exec.LookPath(base); err == nil {
			return p
		}
	}
	return ""
}
func executableDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	d := filepath.Dir(p)
	abs, err := filepath.Abs(d)
	if err != nil {
		return d
	}
	return abs
}
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}
func kb(n int64) float64 { return float64(n) / 1024.0 }
func mb(n int64) float64 { return float64(n) / 1024.0 / 1024.0 }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
