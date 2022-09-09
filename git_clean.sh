#!/bin/bash
error=1
if [ -n "$1" ]; then
    if [ $1 == "0" ]; then
        error=0
        echo "git rev-list --objects --all"
        git rev-list --objects --all
        while :; do
            read -p "输入需要清理的文件或目录的路径(输入E/e以结束)：" input
            if [ $input == "E" ] || [ $input == "e" ]; then
                break
            else
                git filter-branch -f --tree-filter 'rm -rf $input' --tag-name-filter cat -- --all
            fi
        done
        git push origin --tags --force
        git push origin --all --force
        echo -e "\e[31m建议登录Gitee，使用存储库GC以清理悬空文件，压缩存储库对象，减少存储库磁盘占用。\e[0m"
    elif [ $1 == "1" ]; then
        error=0
        git checkout --orphan latest_branch
        git add -A
        git commit -am "clean"
        git branch -D master
        git branch -m master
        git push -f origin master
        git branch --set-upstream-to=origin/master master
    fi
elif [ ! -n "$1" ]; then
    error=0
    git reset origin
    git checkout .
    git init
    git reflog expire --expire=now --all
    git gc --aggressive --prune=now
    git repack -a -d -l
fi
if [ $error -eq 1 ]; then
    echo "./git_clean.sh 清理本地仓库"
    echo "./git_clean.sh 0 手动清理远程仓库"
    echo "./git_clean.sh 1 自动清理远程仓库"
fi
