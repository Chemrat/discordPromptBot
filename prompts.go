package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"math/rand"
	"sync"
)

var saveFile = "save.json"
var prompts = make([]Prompt, 0, 0)
var mutex = sync.Mutex{}

var aclFile = "acl.json"
var admins = make([]string, 0, 0)

type Prompt struct {
	Text     string `json:"Text"`
	Author   string `json:"Author"`
	AuthorID string `json:"AuthorID"`
}

func RestorePrompts() {
	mutex.Lock()
	defer mutex.Unlock()

	file, err := ioutil.ReadFile(saveFile)
	if err == nil {
		err = json.Unmarshal(file, &prompts)
		if err != nil {
			log.Fatal(err)
		}

		log.Println("Restored prompts: ", prompts)
	} else {
		log.Println("Can't restore prompts: ", err)
	}
}

func RestoreACL() {
	file, err := ioutil.ReadFile(aclFile)
	if err == nil {
		err = json.Unmarshal(file, &admins)
		if err != nil {
			log.Fatal(err)
		}

		log.Println("Restored acl: ", admins)
	} else {
		log.Println("Can't restore acl: ", err)
	}
}

func SaveACL() (err error) {
	exportedJSON, err := json.Marshal(admins)
	if err != nil {
		err = errors.New("Failed to serialize list: " + err.Error())
		return
	}

	err = ioutil.WriteFile(aclFile, exportedJSON, 0644)

	if err != nil {
		err = errors.New("Failed to write acl file: " + err.Error())
	}
	return
}

func AddToACL(userID string, requestedBy string) (err error) {
	if !isAdmin(requestedBy) {
		err = errors.New("not allowed: your user ID is not on service ACL")
		return
	}

	admins = append(admins, userID)
	err = SaveACL()
	return
}

func PushPrompt(prompt string, author string, authorID string) (err error) {
	mutex.Lock()
	defer mutex.Unlock()

	newPrompt := Prompt{prompt, author, authorID}
	prompts = append(prompts, newPrompt)

	err = savePromptsToDisk()
	return
}

func PopPrompt(randomPrompt bool) (prompt Prompt, err error) {
	mutex.Lock()
	defer mutex.Unlock()

	if len(prompts) == 0 {
		err = errors.New("Prompt pool is empty")
		return
	}

	if randomPrompt {
		index := rand.Intn(len(prompts))
		prompt = prompts[index]
		prompts = append(prompts[:index], prompts[index+1:]...)
	} else {
		prompt = prompts[0]
		prompts = prompts[1:]
	}

	err = savePromptsToDisk()
	return
}

func DeletePrompt(text string, requestedBy string) (err error) {
	mutex.Lock()
	defer mutex.Unlock()

	for index := range prompts {
		if prompts[index].Text == text {
			if (requestedBy != prompts[index].AuthorID) &&
				(!isAdmin(requestedBy)) {
				err = errors.New("Only author can delete their prompt")
				return
			}

			prompts = append(prompts[:index], prompts[index+1:]...)
			err = savePromptsToDisk()
			return
		}
	}

	err = errors.New("No such prompt")
	return
}

func PurgePrompts(requestedBy string) (err error) {
	mutex.Lock()
	defer mutex.Unlock()

	if isAdmin(requestedBy) {
		prompts = prompts[:0]
		err = savePromptsToDisk()
		return
	}

	err = errors.New("Not allowed. Your user ID is not on service ACL.")
	return
}

func isAdmin(id string) bool {
	for _, record := range admins {
		if record == id {
			return true
		}
	}
	return false
}

func savePromptsToDisk() (err error) {
	exportedJSON, err := json.Marshal(prompts)
	if err != nil {
		err = errors.New("Failed to serialize list: " + err.Error())
		return
	}

	err = ioutil.WriteFile(saveFile, exportedJSON, 0644)

	if err != nil {
		err = errors.New("Failed to write savefile: " + err.Error())
	}
	return
}
