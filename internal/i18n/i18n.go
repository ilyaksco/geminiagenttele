package i18n

import (
	"encoding/json"
	"io"
	"log"
	"os"
)

type I18n struct {
	langs map[string]map[string]string
}

func New() *I18n {
	i := &I18n{
		langs: make(map[string]map[string]string),
	}
	i.loadLang("en", "locales/en.json")
	i.loadLang("id", "locales/id.json")
	return i
}

func (i *I18n) loadLang(code, path string) {
	file, err := os.Open(path)
	if err != nil {
		log.Printf("Failed to open locale file %s: %v\n", path, err)
		return
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		log.Printf("Failed to read locale file %s: %v\n", path, err)
		return
	}

	var translations map[string]string
	err = json.Unmarshal(bytes, &translations)
	if err != nil {
		log.Printf("Failed to parse locale file %s: %v\n", path, err)
		return
	}

	i.langs[code] = translations
	log.Printf("Successfully loaded locale: %s\n", code)
}

func (i *I18n) Get(lang, key string) string {
	if dict, ok := i.langs[lang]; ok {
		if val, ok2 := dict[key]; ok2 {
			return val
		}
	}
	return key
}