package server

import (
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

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yankeguo/rg"
	v1 "github.com/yankeguo/skopeo-machine/api/skopeo_machine/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

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
		TTLSeconds  int32  `json:"ttlSeconds" default:"86400"`
		MultiArch   string `json:"multiArch" default:"system"`
		AuthfileSrc string `json:"authfileSrc"`
		AuthfileDst string `json:"authfileDst"`
	} `json:"copy"`
}

func LoadConf() (conf Conf, err error) {
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

func CreateKubernetesClient() (client *kubernetes.Clientset, err error) {
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

type Options struct {
	Conf             Conf
	KubernetesClient *kubernetes.Clientset
}

type App struct {
	opts Options
	lock *sync.Mutex
}

func (s *App) SkopeoMachineV1CopyPost(c *gin.Context) {
	var req v1.CopyJob

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, v1.Error{
			Message: err.Error(),
		})
		return
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	jobId := createId()

	var err error
	defer func() {
		if err == nil {
			c.JSON(http.StatusOK, v1.CopyJobResponse{
				JobId: jobId,
			})
		} else {
			c.JSON(http.StatusInternalServerError, v1.Error{
				Message: err.Error(),
			})
		}
	}()
	defer rg.Guard(&err)

	log.Printf("copy from %s to %s", req.Source, req.Target)

	req.Source = standardizeImage(req.Source)
	req.Target = standardizeImage(req.Target)

	sourceDigest := sha1sum([]byte(req.Source))
	targetDigest := sha1sum([]byte(req.Target))

	jobLabels := map[string]string{
		"com.yankeguo.skopeo-machine/copy.source-image": sourceDigest,
		"com.yankeguo.skopeo-machine/copy.target-image": targetDigest,
	}

	jobAnnotations := map[string]string{
		"com.yankeguo.skopeo-machine/copy.source-image": req.Source,
		"com.yankeguo.skopeo-machine/copy.target-image": req.Target,
	}

	existed := rg.Must(s.opts.KubernetesClient.BatchV1().Jobs(s.opts.Conf.Job.Namespace).List(c.Request.Context(), metav1.ListOptions{
		LabelSelector: formatSelector(jobLabels),
	})).Items

	var existedValid bool

	for _, job := range existed {
		if job.Status.Active > 0 {
			existedValid = true
		} else {
			if job.Status.CompletionTime != nil {
				existedValid = true
			} else {
				rg.Must0(s.opts.KubernetesClient.BatchV1().Jobs(s.opts.Conf.Job.Namespace).Delete(c.Request.Context(), job.Name, metav1.DeleteOptions{
					PropagationPolicy: ptr(metav1.DeletePropagationBackground),
				}))
			}
		}
	}

	if existedValid {
		log.Println("job active or still valid, skipping")
		return
	}

	job := &batchv1.Job{}
	job.ObjectMeta.Name = "skopeo-copy-" + createId()
	job.ObjectMeta.Namespace = s.opts.Conf.Job.Namespace
	job.ObjectMeta.Labels = jobLabels
	job.ObjectMeta.Annotations = jobAnnotations
	job.Spec.TTLSecondsAfterFinished = ptr(s.opts.Conf.Copy.TTLSeconds)
	job.Spec.Template.ObjectMeta.Labels = jobLabels
	job.Spec.Template.ObjectMeta.Annotations = jobAnnotations
	job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
	job.Spec.Template.Spec.ImagePullSecrets = s.opts.Conf.Job.ImagePullSecrets
	if s.opts.Conf.Copy.AuthfileSrc != "" {
		job.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "authfile-src",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: s.opts.Conf.Copy.AuthfileSrc,
					},
				},
			},
		}
	}
	if s.opts.Conf.Copy.AuthfileDst != "" {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "authfile-dst",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: s.opts.Conf.Copy.AuthfileDst,
				},
			},
		})
	}
	container := corev1.Container{
		Name:            "skopeo-copy",
		Image:           s.opts.Conf.Job.Image,
		ImagePullPolicy: s.opts.Conf.Job.ImagePullPolicy,
	}
	if s.opts.Conf.Copy.AuthfileSrc != "" {
		container.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "authfile-src",
				MountPath: "/authfile-src",
			},
		}
	}
	if s.opts.Conf.Copy.AuthfileDst != "" {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "authfile-dst",
			MountPath: "/authfile-dst",
		})
	}
	args := []string{
		"copy",
	}
	if s.opts.Conf.Copy.MultiArch != "" {
		args = append(args, "--multi-arch="+s.opts.Conf.Copy.MultiArch)
	}
	if s.opts.Conf.Copy.AuthfileSrc != "" {
		args = append(args, "--src-authfile=/authfile-src/.dockerconfigjson")
	}
	if s.opts.Conf.Copy.AuthfileDst != "" {
		args = append(args, "--dest-authfile=/authfile-dst/.dockerconfigjson")
	}
	args = append(args, "docker://"+req.Source, "docker://"+req.Target)

	container.Args = args

	job.Spec.Template.Spec.Containers = []corev1.Container{container}

	rg.Must(s.opts.KubernetesClient.BatchV1().Jobs(s.opts.Conf.Job.Namespace).Create(c.Request.Context(), job, metav1.CreateOptions{}))
}

var _ v1.DefaultAPI = (*App)(nil)

func CreateApp(opts Options) (s *App) {
	return &App{
		opts: opts,
		lock: &sync.Mutex{},
	}
}
