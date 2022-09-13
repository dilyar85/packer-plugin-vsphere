# Not this is an example template file to show how to use the packer-vsphere-supervisor plugin.
# Please repace the config values accordingly to build a VM it in your own supervisor cluster.

source "vsphere-supervisor" "vm" {
  image_name = "centos-stream8-cloudinit-22.1"
  class_name = "best-effort-large"
  storage_class = "wcplocal-storage-profile"
  kubeconfig_path = "~/.kube/config"
  k8s_namespace = "nginx-test"
  source_name = "packer-vsphere-supervisor-example"
  network_type = "nsx-t"
  ssh_username = "root"
  ssh_password = "root"
  watch_source_timeout_sec = 600
  keep_source = true
}

build {
  sources = ["source.vsphere-supervisor.vm"]
  provisioner "shell" {
    inline = [
      "yum install -qy nginx",
      "systemctl restart nginx",
      "systemctl status nginx",
      "echo 'Testing Nginx connectivity...'",
      "curl -sI http://localhost:80",
    ]
  }
  provisioner "ansible" {
    playbook_file = "cleanup-playbook.yml"
  }
}
