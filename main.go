package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

type imageData struct {
	URL       string
	Body      []byte
	Extension string
	Size      image.Config
	Quality   int
}

var client = &http.Client{}

var (
	errNoLargerAvailable = errors.New("there is no large image")
	errCaptcha           = errors.New("response was captcha page")
)

func uploadImage(filename string) (contents []byte, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileContents, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, err
	}

	fileStat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	writer := multipart.NewWriter(buf)
	part, err := writer.CreateFormFile("encoded_image", fileStat.Name())
	if err != nil {
		return nil, err
	}
	_, err = part.Write(fileContents)
	if err != nil {
		return nil, err
	}

	err = writer.WriteField("image_url", "")
	if err != nil {
		return nil, err
	}
	err = writer.WriteField("filename", "")
	if err != nil {
		return nil, err
	}
	err = writer.WriteField("hl", "ko")
	if err != nil {
		return nil, err
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, "https://images.google.com/searchbyimage/upload", buf)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())
	req.Header.Add("origin", "https://images.google.com/")
	req.Header.Add("referer", "https://images.google.com/")
	req.Header.Add("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.4951.54 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contents, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return contents, nil
}

func getImage(url string) (data *imageData, err error) {
	data = &imageData{}

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	imageDecode, ext, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	data.URL = url
	data.Body = body
	data.Extension = ext
	data.Size = imageDecode
	data.Quality = data.Size.Height * data.Size.Width

	return data, nil
}

func getImageList(contents []byte) (data []*imageData, err error) {
	var largeImgURL string
	r, _ := regexp.Compile(`(/search\?.*?simg:.*?)">`)

	for _, i := range r.FindAllStringSubmatch(string(contents), -1) {
		if len(i) < 2 {
			continue
		}

		if strings.Contains(i[1], ",isz:l") {
			largeImgURL = "https://google.com" + html.UnescapeString(i[1])

			break
		}
	}

	if len(largeImgURL) == 0 && bytes.Contains(contents, []byte("captcha")) {
		return nil, errCaptcha
	} else if len(largeImgURL) == 0 {
		return nil, errNoLargerAvailable
	}

	req, err := http.NewRequest(http.MethodGet, largeImgURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("origin", "https://images.google.com/")
	req.Header.Add("referer", "https://images.google.com/")
	req.Header.Add("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.4951.54 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	imgInfo, _ := regexp.Compile(`\["(https://.*?.)",(\d+),(\d+)\]`)

	for _, i := range imgInfo.FindAllStringSubmatch(string(body), -1) {
		if len(i) < 4 {
			continue
		}

		urlUnquote, err := strconv.Unquote("\"" + i[1] + "\"")
		if err != nil {
			continue
		}

		imgURL, err := url.Parse(urlUnquote)
		if err != nil {
			continue
		}

		imgHeight, err := strconv.Atoi(i[2])
		if err != nil {
			continue
		}
		imgWidth, err := strconv.Atoi(i[3])
		if err != nil {
			continue
		}

		data = append(data, &imageData{
			URL:     imgURL.String(),
			Quality: imgHeight * imgWidth,
		})
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i].Quality > data[j].Quality
	})

	return data, nil
}

func getImageSizeFromFile(filename string) (data image.Config, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return image.Config{}, err
	}
	defer file.Close()

	data, _, err = image.DecodeConfig(file)
	if err != nil {
		return image.Config{}, err
	}

	return data, nil
}

func main() {
	path := flag.String("path", "", "A image file or directory path")
	output := flag.String("output", "", "Result output directory path")
	copyInput := flag.Bool("copy", true, "Copy the original image if not higher resolution available")
	logLevel := flag.String("log-level", "error", "Set the level of log output: (info, warn, error)")
	flag.Parse()

	switch strings.ToLower(*logLevel) {
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	default:
		flag.PrintDefaults()
	}

	if len(*path) == 0 || len(*output) == 0 {
		flag.PrintDefaults()

		return
	}

	if err := os.MkdirAll(*output, os.ModePerm); err != nil {
		log.Error("output path must be directory")
		return
	}

	if err := filepath.Walk(*path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			switch filepath.Ext(path) {
			case ".jpg", ".png", ".jpeg":
				log.Infof("[%s] Getting original image info...", path)
				imageSize, err := getImageSizeFromFile(path)
				if err != nil {
					return err
				}

				log.Infof("[%s] Uploading to google image server...", path)
				contents, err := uploadImage(path)
				if err != nil {
					log.Fatal(err)
				}

				justcopy := false

				data, err := getImageList(contents)
				if err != nil {
					if errors.Is(errNoLargerAvailable, err) {
						justcopy = true
						file, _ := os.Open(path)

						imageBody, _ := ioutil.ReadAll(file)
						file.Close()

						data = append(data, &imageData{
							URL:       path,
							Body:      imageBody,
							Extension: filepath.Ext(path),
							Size:      imageSize,
						})
					} else if errors.Is(errCaptcha, err) {
						log.Fatal("received a captcha page, stopping")
					} else {
						log.Fatal(err)
					}
				}

				for _, i := range data {
					if justcopy && *copyInput {
						log.Warnf("[%s] High resolution image does not found, so just copyed: %s", path, i.URL)
						if err := ioutil.WriteFile(fmt.Sprintf("%s/%s", *output, info.Name()), i.Body, os.ModePerm); err != nil {
							log.Error(err)
						}

						break
					}

					log.Infof("[%s] Image URL: %s", path, i.URL)
					imageInfo, err := getImage(i.URL)
					if err != nil {
						log.Warn("This URL is not available, so try again with another URL")
						continue
					}

					newFilename := strings.ReplaceAll(info.Name(), filepath.Ext(path), "."+imageInfo.Extension)

					log.Infof("[%s] Saving high resolution image...", path)
					if err := ioutil.WriteFile(fmt.Sprintf("%s/%s", *output, newFilename), imageInfo.Body, os.ModePerm); err != nil {
						log.Error(err)
					}
					log.Infof("[%s] Saved: %s (%dx%d -> %dx%d)", path, newFilename, imageSize.Width, imageSize.Height, imageInfo.Size.Width, imageInfo.Size.Height)

					break
				}
			default:
				log.Infof("[%s] Skip !", path)
			}
		}

		return nil
	}); err != nil {
		log.Error("please change the file or directory path and try again")
	}
}
