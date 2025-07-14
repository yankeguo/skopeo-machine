package main

import (
	"log"
	"os"

	"github.com/yankeguo/rg"
	v1 "github.com/yankeguo/skopeo-machine/api/skopeo_machine/v1"
	"github.com/yankeguo/skopeo-machine/server"
)

func main() {
	var err error
	defer func() {
		if err == nil {
			return
		}
		log.Println("exit with error:", err.Error())
		os.Exit(1)
	}()
	defer rg.Guard(&err)

	app := server.CreateApp(server.Options{
		Conf:             rg.Must(server.LoadConf()),
		KubernetesClient: rg.Must(server.CreateKubernetesClient()),
	})

	r := v1.NewRouter(v1.ApiHandleFunctions{
		DefaultAPI: app,
	})

	err = r.Run(":8080")
}
