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
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type imageData struct {
	URL       string
	Body      []byte
	Extension string
	Size      image.Config
	Quality   int
}

var client = &http.Client{}

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
	part.Write(fileContents)

	writer.WriteField("image_url", "")
	writer.WriteField("filename", "")
	writer.WriteField("hl", "ko")

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

	if len(largeImgURL) == 0 {
		return nil, errors.New("there is no large image")
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
	flag.Parse()

	if len(*path) == 0 {
		flag.PrintDefaults()

		return
	}

	os.Mkdir("result", os.ModePerm)

	if err := filepath.Walk(*path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			switch filepath.Ext(path) {
			case ".jpg", ".png", ".jpeg":
				log.Printf("[%s] Getting original image info...", path)
				imageSize, err := getImageSizeFromFile(path)
				if err != nil {
					return err
				}

				log.Printf("[%s] Uploading to google image server...", path)
				contents, err := uploadImage(path)
				if err != nil {
					panic(err)
				}

				justcopy := false

				data, err := getImageList(contents)
				if err != nil {
					if err.Error() == "there is no large image" {
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
					} else {
						panic(err)
					}
				}

				for _, i := range data {
					if justcopy {
						log.Printf("[%s] High resolution image does not found, so just copyed: %s", path, i.URL)
						if err := ioutil.WriteFile(fmt.Sprintf("result/%s", info.Name()), i.Body, os.ModePerm); err != nil {
							fmt.Println(err)
						}

						break
					}

					log.Printf("[%s] Image URL: %s", path, i.URL)
					imageInfo, err := getImage(i.URL)
					if err != nil {
						log.Printf("This URL is not available, so try again with another URL")

						continue
					}

					newFilename := strings.ReplaceAll(info.Name(), filepath.Ext(path), "."+imageInfo.Extension)

					log.Printf("[%s] Saving high resolution image...", path)
					if err := ioutil.WriteFile(fmt.Sprintf("result/%s", newFilename), imageInfo.Body, os.ModePerm); err != nil {
						fmt.Println(err)
					}
					log.Printf("[%s] Saved: %s (%dx%d -> %dx%d)", path, newFilename, imageSize.Width, imageSize.Height, imageInfo.Size.Width, imageInfo.Size.Height)

					break
				}
			default:
				log.Printf("[%s] Skip !", path)
			}
		}

		return nil
	}); err != nil {
		log.Println(errors.New("please change the file or directory path and try again"))
	}
}
