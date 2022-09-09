#!/bin/bash
error=1
curr_path=$(
	cd $(dirname $0)
	pwd
)
dir_name="${curr_path##*/}"
function check_push() {
	if [ "$dir_name" == "shell" ]; then
		echo "正在检查本地文件或目录..."
		for file in $(ls $1); do
			ext=${file#*.}
			if [ "$ext" != "sh" ] && [ "$ext" != "md" ] && [ "$ext" != "termux" ] && [ "$ext" != "thesaurus" ] && [ "$ext" != "icon" ] && [ "$ext" != "data" ]; then
				echo "发现未跟踪的文件$file，已终止！"
				exit
			fi
		done
	fi
}
if [ ! -n "$1" ]; then
	error=0
	if [ ! -f "${HOME}/private_shell/gitee.txt" ]; then
		echo "${HOME}/private_shell/gitee.txt不存在！"
		echo "${HOME}/private_shell/gitee.txt第1行为邮箱"
		echo "${HOME}/private_shell/gitee.txt第2行为用户名"
		echo "${HOME}/private_shell/gitee.txt第3行为密码"
		exit
	fi
	Email=$(sed -n 1p "${HOME}/private_shell/gitee.txt")
	Username=$(sed -n 2p "${HOME}/private_shell/gitee.txt")
	Password=$(sed -n 3p "${HOME}/private_shell/gitee.txt")
	git config --global user.email "$Email"
	git config --global user.name "$Username"
	git config --global push.default matching
#	check_push
	git status
	git add .
	git commit -m "push"
	expect <<EOF
spawn git push
expect "Username"
send "$Username\n"
expect "Password"
send "$Password\n"
expect eof
EOF
elif [ -n "$1" ]; then
	if [ $1 == "0" ]; then
		error=0
		if [ ! -f "${HOME}/private_shell/gitee.txt" ]; then
			echo "${HOME}/private_shell/gitee.txt不存在！"
			echo "${HOME}/private_shell/gitee.txt第1行为邮箱"
			echo "${HOME}/private_shell/gitee.txt第2行为用户名"
			echo "${HOME}/private_shell/gitee.txt第3行为密码"
			exit
		fi
		Email=$(sed -n 1p "${HOME}/private_shell/gitee.txt")
		Username=$(sed -n 2p "${HOME}/private_shell/gitee.txt")
		Password=$(sed -n 3p "${HOME}/private_shell/gitee.txt")
		git config --global user.email "$Email"
		git config --global user.name "$Username"
		git config --global push.default matching
		#check_push
		git status
		git add .
		git commit -m "push"
		git push
	elif [ $1 == "1" ]; then
		error=0
		if [ ! -f "${HOME}/private_shell/github.txt" ]; then
			echo "${HOME}/private_shell/github.txt不存在！"
			echo "${HOME}/private_shell/github.txt第1行为邮箱"
			echo "${HOME}/private_shell/github.txt第2行为用户名"
			echo "${HOME}/private_shell/github.txt第3行为个人访问令牌（PAT）"
			echo "关于个人访问令牌（PAT）"
			echo "https://docs.github.com/cn/github/authenticating-to-github/keeping-your-account-and-data-secure/creating-a-personal-access-token"
			exit
		fi
		Email=$(sed -n 1p "${HOME}/private_shell/github.txt")
		Username=$(sed -n 2p "${HOME}/private_shell/github.txt")
		Password=$(sed -n 3p "${HOME}/private_shell/github.txt")
		git config --global user.email "$Email"
		git config --global user.name "$Username"
		git config --global push.default matching
		check_push
		git status
		git add .
		git commit -m "push"
		expect <<EOF
spawn git push
expect "Username"
send "$Username\n"
expect "Password"
send "$Password\n"
expect eof
EOF
	elif [ $1 == "2" ]; then
		error=0
		if [ ! -f "${HOME}/private_shell/github.txt" ]; then
			echo "${HOME}/private_shell/github.txt不存在！"
			echo "${HOME}/private_shell/github.txt第1行为邮箱"
			echo "${HOME}/private_shell/github.txt第2行为用户名"
			echo "${HOME}/private_shell/github.txt第3行为个人访问令牌（PAT）"
			echo "关于个人访问令牌（PAT）"
			echo "https://docs.github.com/cn/github/authenticating-to-github/keeping-your-account-and-data-secure/creating-a-personal-access-token"
			exit
		fi
		Email=$(sed -n 1p "${HOME}/private_shell/github.txt")
		Username=$(sed -n 2p "${HOME}/private_shell/github.txt")
		Password=$(sed -n 3p "${HOME}/private_shell/github.txt")
		git config --global user.email "$Email"
		git config --global user.name "$Username"
		git config --global push.default matching
		check_push
		git status
		git add .
		git commit -m "push"
		echo -e "\e[31m注意：请用个人访问令牌（PAT）代替密码\e[0m"
		echo "关于个人访问令牌（PAT）"
		echo "https://docs.github.com/cn/github/authenticating-to-github/keeping-your-account-and-data-secure/creating-a-personal-access-token"
		git push
	fi
fi
if [ $error -eq 1 ]; then
	echo "./git_push.sh 免密push（Gitee）"
	echo "./git_push.sh 0 常规push（Gitee）"
	echo "./git_push.sh 1 免密push（GitHub）"
	echo "./git_push.sh 2 常规push（GitHub）"
fi
