set -e

remotes='https://gitlab.com/gitlab-org/gitlab-workhorse.git'

main() {
  get_version
  tag_name="v${version}"
  git tag -m "Version ${version}" -a ${tag_name}
  git show ${tag_name}
  echo
  echo "Does this look OK? Enter 'yes' to push to ${remotes}"
  read confirmation
  if [ "x${confirmation}" != xyes ] ; then
    echo "Aborting"
    exit 1
  fi
  for r in ${remotes}; do
    git push "${r}" HEAD ${tag_name}
  done
}

get_version() {
  v=$(sed 1q VERSION)
  if ! echo "${v}" | grep -q '^[0-9]\+\.[0-9]\+\.[0-9]\+$' ; then
    echo "Invalid VERSION: ${v}"
    exit 1
  fi
  version="${v}"
}

main