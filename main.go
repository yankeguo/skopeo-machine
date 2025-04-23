package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/creasty/defaults"
	"github.com/yankeguo/rg"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Conf struct {
	Job struct {
		Namespace        string                        `json:"namespace"`
		Image            string                        `json:"image" default:"quay.io/skopeo/stable:latest"`
		ImagePullPolicy  corev1.PullPolicy             `json:"imagePullPolicy"`
		ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets"`
	} `json:"job"`

	Copy struct {
		AuthfileSrc string `json:"authfileSrc"`
		AuthfileDst string `json:"authfileDst"`
	} `json:"copy"`
}

func createKubernetesClient() (client *kubernetes.Clientset, err error) {
	defer rg.Guard(&err)

	var cfg *rest.Config

	envKubeconfig := os.Getenv("KUBECONFIG")

	if envKubeconfig != "" {
		log.Println("KUBECONFIG is set, using kubeconfig")
		cfg = rg.Must(clientcmd.BuildConfigFromFlags("", envKubeconfig))
	} else {
		log.Println("KUBECONFIG is not set, using in-cluster config")
		cfg = rg.Must(rest.InClusterConfig())
	}

	client = rg.Must(kubernetes.NewForConfig(cfg))

	return
}

func createConf() (conf Conf, err error) {
	defer rg.Guard(&err)

	var optConf string

	flag.StringVar(&optConf, "conf", "", "config file")
	flag.Parse()

	buf := rg.Must(os.ReadFile(optConf))

	rg.Must0(json.Unmarshal(buf, &conf))

	rg.Must0(defaults.Set(&conf))

	return
}

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

	conf := rg.Must(createConf())

	client := rg.Must(createKubernetesClient())

	http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		defer func() {
			if err == nil {
				w.Write([]byte("ok"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(err.Error()))
			}
		}()
		defer rg.Guard(&err)

		var data struct {
			Action string `json:"action"`
			Source string `json:"source"`
			Target string `json:"target"`
		}
		rg.Must0(json.NewDecoder(r.Body).Decode(&data))

		switch data.Action {
		case "copy":
			{
			}
		}
	}))
}
