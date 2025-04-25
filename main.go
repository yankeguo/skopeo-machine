package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/creasty/defaults"
	"github.com/google/uuid"
	"github.com/yankeguo/rg"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Conf struct {
	Auth struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"auth"`

	Job struct {
		Namespace        string                        `json:"namespace"`
		Image            string                        `json:"image" default:"quay.io/skopeo/stable:latest"`
		ImagePullPolicy  corev1.PullPolicy             `json:"imagePullPolicy"`
		ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets"`
	} `json:"job"`

	Copy struct {
		MultiArch   string `json:"multiArch" default:"system"`
		AuthfileSrc string `json:"authfileSrc"`
		AuthfileDst string `json:"authfileDst"`
	} `json:"copy"`
}

func ptr[T any](v T) *T {
	return &v
}

func createId() string {
	id := rg.Must(uuid.NewV7())
	return strings.ReplaceAll(id.String(), "-", "")
}

func formatSelector(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	var sb strings.Builder
	for k, v := range labels {
		if sb.Len() > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%s=%s", k, v))
	}
	return sb.String()
}

func sha1sum(b []byte) string {
	h := sha1.New()
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func standardizeImage(image string) string {
	if !strings.Contains(image, ":") {
		image += ":latest"
	}

	splits := strings.Split(image, "/")

	// single part
	if len(splits) < 2 {
		return "docker.io/library/" + image
	}

	// custom domain
	if strings.Contains(splits[0], ".") || strings.Contains(splits[0], ":") {
		// first part is docker.io and only 2 parts
		if len(splits) == 2 && splits[0] == "docker.io" {
			return "docker.io/library/" + splits[1]
		}
		return image
	}

	return "docker.io/" + image
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

	flag.StringVar(&optConf, "conf", "config.json", "config file")
	flag.Parse()

	buf := rg.Must(os.ReadFile(optConf))

	rg.Must0(json.Unmarshal(buf, &conf))

	rg.Must0(defaults.Set(&conf))

	if conf.Job.Namespace == "" {
		conf.Job.Namespace = os.Getenv("POD_NAMESPACE")
	}

	if conf.Job.Namespace == "" {
		buf, _ = os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		conf.Job.Namespace = string(buf)
	}

	if conf.Job.Namespace == "" {
		err = errors.New("namespace is empty")
		return
	}

	return
}

var (
	gConf   Conf
	gClient *kubernetes.Clientset
	gLocker = &sync.Mutex{}
)

type copyOptions struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

func doCopy(ctx context.Context, opts copyOptions) (err error) {
	gLocker.Lock()
	defer gLocker.Unlock()
	defer rg.Guard(&err)

	log.Printf("copy from %s to %s", opts.Source, opts.Target)

	opts.Source = standardizeImage(opts.Source)
	opts.Target = standardizeImage(opts.Target)

	sourceDigest := sha1sum([]byte(opts.Source))
	targetDigest := sha1sum([]byte(opts.Target))

	jobLabels := map[string]string{
		"com.yankeguo.skopeo-machine/copy.source-image": sourceDigest,
		"com.yankeguo.skopeo-machine/copy.target-image": targetDigest,
	}

	jobAnnotations := map[string]string{
		"com.yankeguo.skopeo-machine/copy.source-image": opts.Source,
		"com.yankeguo.skopeo-machine/copy.target-image": opts.Target,
	}

	existed := rg.Must(gClient.BatchV1().Jobs(gConf.Job.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: formatSelector(jobLabels),
	})).Items

	var existedValid bool

	for _, job := range existed {
		if job.Status.Active > 0 {
			existedValid = true
		} else {
			if job.Status.CompletionTime != nil {
				if time.Now().Sub(job.Status.CompletionTime.Time) > time.Hour*24 {
					rg.Must0(gClient.BatchV1().Jobs(gConf.Job.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{
						PropagationPolicy: ptr(metav1.DeletePropagationBackground),
					}))
				} else {
					existedValid = true
				}
			} else {
				rg.Must0(gClient.BatchV1().Jobs(gConf.Job.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{
					PropagationPolicy: ptr(metav1.DeletePropagationBackground),
				}))
			}
		}
	}

	if existedValid {
		log.Println("job already exists, skip")
		return
	}

	job := &batchv1.Job{}
	job.ObjectMeta.Name = "skopeo-copy-" + createId()
	job.ObjectMeta.Namespace = gConf.Job.Namespace
	job.ObjectMeta.Labels = jobLabels
	job.ObjectMeta.Annotations = jobAnnotations
	job.Spec.TTLSecondsAfterFinished = ptr[int32](600)
	job.Spec.Template.ObjectMeta.Labels = jobLabels
	job.Spec.Template.ObjectMeta.Annotations = jobAnnotations
	job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
	job.Spec.Template.Spec.ImagePullSecrets = gConf.Job.ImagePullSecrets
	if gConf.Copy.AuthfileSrc != "" {
		job.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "authfile-src",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: gConf.Copy.AuthfileSrc,
					},
				},
			},
		}
	}
	if gConf.Copy.AuthfileDst != "" {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "authfile-dst",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: gConf.Copy.AuthfileDst,
				},
			},
		})
	}
	container := corev1.Container{
		Name:            "skopeo-copy",
		Image:           gConf.Job.Image,
		ImagePullPolicy: gConf.Job.ImagePullPolicy,
	}
	if gConf.Copy.AuthfileSrc != "" {
		container.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "authfile-src",
				MountPath: "/authfile-src",
			},
		}
	}
	if gConf.Copy.AuthfileDst != "" {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "authfile-dst",
			MountPath: "/authfile-dst",
		})
	}
	args := []string{
		"copy",
	}
	if gConf.Copy.MultiArch != "" {
		args = append(args, "--multi-arch="+gConf.Copy.MultiArch)
	}
	if gConf.Copy.AuthfileSrc != "" {
		args = append(args, "--src-authfile=/authfile-src/.dockerconfigjson")
	}
	if gConf.Copy.AuthfileDst != "" {
		args = append(args, "--dest-authfile=/authfile-dst/.dockerconfigjson")
	}
	args = append(args, "docker://"+opts.Source, "docker://"+opts.Target)

	container.Args = args

	job.Spec.Template.Spec.Containers = []corev1.Container{container}

	rg.Must(gClient.BatchV1().Jobs(gConf.Job.Namespace).Create(ctx, job, metav1.CreateOptions{}))

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

	gConf = rg.Must(createConf())

	gClient = rg.Must(createKubernetesClient())

	http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		defer func() {
			if err == nil {
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
		}()
		defer rg.Guard(&err)

		if username, password, _ := r.BasicAuth(); username != gConf.Auth.Username || password != gConf.Auth.Password {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		var data struct {
			Action string `json:"action"`
			Source string `json:"source"`
			Target string `json:"target"`
		}
		rg.Must0(json.NewDecoder(r.Body).Decode(&data))

		switch data.Action {
		case "copy":
			{
				if data.Source == "" {
					http.Error(w, "source is empty", http.StatusBadRequest)
					return
				}
				if data.Target == "" {
					http.Error(w, "target is empty", http.StatusBadRequest)
					return
				}
				rg.Must0(doCopy(r.Context(), copyOptions{
					Source: data.Source,
					Target: data.Target,
				}))
				http.Error(w, http.StatusText(http.StatusOK), http.StatusOK)
				return
			}
		default:
			http.Error(w, "action not supported", http.StatusBadRequest)
			return
		}
	}))

}
